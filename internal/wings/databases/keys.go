package databases

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"
)

// The key browser (docs/DATABASES.md#the-key-and-document-browsers): a
// Redis database's keys, seen and changed through typed commands, as the
// database's own user through the instance's socket, so nothing reaches
// further than that user's rights. Keys are binary-safe: one that isn't
// UTF-8 travels as hex.

// Bounds on the key browser.
const (
	maxKeysPage  = 500     // keys in one page
	maxItems     = 500     // a collection's items in one look
	maxValue     = 1 << 20 // bytes of a string shown, then it's cut
	maxArgs      = 64      // arguments of one command
	maxReplyKeys = 2000    // elements of a command's reply, then it's cut
)

// KeyInfo is a key in a page of them.
type KeyInfo struct {
	Key string `json:"key"`
	// Hex is the key in hex when it isn't UTF-8 (Key is then a sketch).
	Hex  string `json:"hex,omitempty"`
	Type string `json:"type"` // string, hash, list, set, zset, stream, ReJSON-RL…
	TTL  int64  `json:"ttl"`  // milliseconds; -1 is none
}

// KeysResult is a page of keys.
type KeysResult struct {
	Keys []KeyInfo `json:"keys"`
	// Cursor fetches the next page; "0" is the end.
	Cursor string `json:"cursor"`
	Total  int64  `json:"total"` // keys in the database
}

// KeysQuery asks for a page of keys.
type KeysQuery struct {
	Pattern string `json:"pattern,omitempty"` // a glob; "" is every key
	Cursor  string `json:"cursor,omitempty"`
	Count   int    `json:"count,omitempty"`
}

// Item is one element of a key's value.
type Item struct {
	Field string   `json:"field,omitempty"` // a hash's field, a stream entry's ID
	Index *int64   `json:"index,omitempty"` // a list's position
	Value string   `json:"value"`
	Score *float64 `json:"score,omitempty"` // a sorted set's
	// Fields are a stream entry's.
	Fields map[string]string `json:"fields,omitempty"`
}

// KeyValue is one key with its value.
type KeyValue struct {
	Key    string `json:"key"`
	Hex    string `json:"hex,omitempty"`
	Type   string `json:"type"`
	TTL    int64  `json:"ttl"`
	Length int64  `json:"length"` // bytes of a string, elements of anything else
	// String is a string's value (or a JSON document's).
	String *string `json:"string,omitempty"`
	Items  []Item  `json:"items,omitempty"`
	// Cursor fetches more items ("" is none).
	Cursor string `json:"cursor,omitempty"`
	Cut    bool   `json:"cut,omitempty"`
}

// KeyRef names a key: Hex wins when set.
type KeyRef struct {
	Key string `json:"key"`
	Hex string `json:"hex,omitempty"`
}

func (k KeyRef) name() (string, error) {
	if k.Hex != "" {
		b, err := hex.DecodeString(k.Hex)
		if err != nil {
			return "", invalid{"the key's hex isn't hex"}
		}
		return string(b), nil
	}
	if k.Key == "" {
		return "", invalid{"name a key"}
	}
	return k.Key, nil
}

func keyNames(k string) (string, string) {
	if utf8.ValidString(k) {
		return k, ""
	}
	return strings.ToValidUTF8(k, "�"), hex.EncodeToString([]byte(k))
}

// redisAs connects to a database's Redis instance as its user.
func (s *Service) redisAs(ctx context.Context, serverID, id string) (*redis.Client, error) {
	r, err := s.database(ctx, serverID, id)
	if err != nil {
		return nil, err
	}
	in, _, err := s.instance(ctx, s.o.Store.Read, r.InstanceID)
	if err != nil {
		return nil, err
	}
	if !isRedis(in.Engine) {
		return nil, invalid{"that isn't a Redis or Valkey database"}
	}
	if s.state(r.InstanceID) != StateRunning {
		return nil, notReady{fmt.Sprintf("%s isn't running; start it first", in.Name)}
	}
	pass, err := s.sealer.open(r.Password)
	if err != nil {
		return nil, err
	}
	return redis.NewClient(&redis.Options{
		Network: "unix", Addr: redisSocket(s.dir(r.InstanceID)), Username: r.Username, Password: pass,
		Dialer: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialUnix(ctx, redisSocket(s.dir(r.InstanceID)))
		},
		DialTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, MaxRetries: -1, DisableIdentity: true,
		PoolSize: 1,
	}), nil
}

// Keys returns a page of a Redis database's keys, with their types and
// times to live.
func (s *Service) Keys(ctx context.Context, serverID, id string, q KeysQuery) (KeysResult, error) {
	c, err := s.redisAs(ctx, serverID, id)
	if err != nil {
		return KeysResult{}, err
	}
	defer func() { _ = c.Close() }()
	count := q.Count
	if count <= 0 || count > maxKeysPage {
		count = 200
	}
	cursor := uint64(0)
	if q.Cursor != "" {
		if cursor, err = strconv.ParseUint(q.Cursor, 10, 64); err != nil {
			return KeysResult{}, invalid{"bad cursor"}
		}
	}
	pattern := q.Pattern
	if pattern == "" {
		pattern = "*"
	}
	// SCAN may return few or none per call; keep going until a page fills
	// or the database ends, within the command's time.
	var keys []string
	deadline := time.Now().Add(10 * time.Second)
	for {
		page, next, err := c.Scan(ctx, cursor, pattern, int64(count)).Result()
		if err != nil {
			return KeysResult{}, err
		}
		keys = append(keys, page...)
		cursor = next
		if cursor == 0 || len(keys) >= count || time.Now().After(deadline) {
			break
		}
	}
	slices.Sort(keys)
	out := KeysResult{Keys: make([]KeyInfo, 0, len(keys)), Cursor: strconv.FormatUint(cursor, 10)}
	pipe := c.Pipeline()
	types := make([]*redis.StatusCmd, len(keys))
	ttls := make([]*redis.DurationCmd, len(keys))
	for i, k := range keys {
		types[i] = pipe.Type(ctx, k)
		ttls[i] = pipe.PTTL(ctx, k)
	}
	size := pipe.DBSize(ctx)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return KeysResult{}, err
	}
	out.Total = size.Val()
	for i, k := range keys {
		name, hx := keyNames(k)
		t := types[i].Val()
		if t == "none" {
			continue // expired or deleted since the scan
		}
		out.Keys = append(out.Keys, KeyInfo{Key: name, Hex: hx, Type: t, TTL: ttlMillis(ttls[i].Val())})
	}
	return out, nil
}

func ttlMillis(d time.Duration) int64 {
	if d < 0 {
		return -1
	}
	return d.Milliseconds()
}

// Key returns one key's value: a string (cut past 1 MiB), or a
// collection's first items, with a cursor for more.
func (s *Service) Key(ctx context.Context, serverID, id string, ref KeyRef, cursor string) (KeyValue, error) {
	key, err := ref.name()
	if err != nil {
		return KeyValue{}, err
	}
	c, err := s.redisAs(ctx, serverID, id)
	if err != nil {
		return KeyValue{}, err
	}
	defer func() { _ = c.Close() }()
	t, err := c.Type(ctx, key).Result()
	if err != nil {
		return KeyValue{}, err
	}
	if t == "none" {
		return KeyValue{}, fmt.Errorf("%w: there's no key %q", ErrNotFound, key)
	}
	ttl, _ := c.PTTL(ctx, key).Result()
	name, hx := keyNames(key)
	v := KeyValue{Key: name, Hex: hx, Type: t, TTL: ttlMillis(ttl)}
	start, _ := strconv.ParseInt(cursor, 10, 64)
	scanCursor, _ := strconv.ParseUint(cursor, 10, 64)
	switch t {
	case "string":
		v.Length, _ = c.StrLen(ctx, key).Result()
		str, err := c.GetRange(ctx, key, 0, maxValue-1).Result()
		if err != nil {
			return v, err
		}
		v.String, v.Cut = &str, v.Length > maxValue
	case "ReJSON-RL":
		str, err := c.Do(ctx, "JSON.GET", key).Text()
		if err != nil {
			return v, err
		}
		v.Length = int64(len(str))
		if len(str) > maxValue {
			str, v.Cut = str[:maxValue], true
		}
		v.String = &str
	case "hash":
		v.Length, _ = c.HLen(ctx, key).Result()
		kv, next, err := c.HScan(ctx, key, scanCursor, "*", maxItems).Result()
		if err != nil {
			return v, err
		}
		for i := 0; i+1 < len(kv); i += 2 {
			v.Items = append(v.Items, Item{Field: kv[i], Value: kv[i+1]})
		}
		if next != 0 {
			v.Cursor = strconv.FormatUint(next, 10)
		}
	case "list":
		v.Length, _ = c.LLen(ctx, key).Result()
		vals, err := c.LRange(ctx, key, start, start+maxItems-1).Result()
		if err != nil {
			return v, err
		}
		for i, val := range vals {
			n := start + int64(i)
			v.Items = append(v.Items, Item{Index: &n, Value: val})
		}
		if start+int64(len(vals)) < v.Length {
			v.Cursor = strconv.FormatInt(start+int64(len(vals)), 10)
		}
	case "set":
		v.Length, _ = c.SCard(ctx, key).Result()
		vals, next, err := c.SScan(ctx, key, scanCursor, "*", maxItems).Result()
		if err != nil {
			return v, err
		}
		slices.Sort(vals)
		for _, val := range vals {
			v.Items = append(v.Items, Item{Value: val})
		}
		if next != 0 {
			v.Cursor = strconv.FormatUint(next, 10)
		}
	case "zset":
		v.Length, _ = c.ZCard(ctx, key).Result()
		vals, err := c.ZRangeWithScores(ctx, key, start, start+maxItems-1).Result()
		if err != nil {
			return v, err
		}
		for _, z := range vals {
			score := z.Score
			v.Items = append(v.Items, Item{Value: fmt.Sprint(z.Member), Score: &score})
		}
		if start+int64(len(vals)) < v.Length {
			v.Cursor = strconv.FormatInt(start+int64(len(vals)), 10)
		}
	case "stream":
		v.Length, _ = c.XLen(ctx, key).Result()
		end := "+"
		if cursor != "" {
			end = "(" + cursor
		}
		msgs, err := c.XRevRangeN(ctx, key, end, "-", maxItems).Result()
		if err != nil {
			return v, err
		}
		for _, m := range msgs {
			fields := map[string]string{}
			for f, val := range m.Values {
				fields[f] = fmt.Sprint(val)
			}
			v.Items = append(v.Items, Item{Field: m.ID, Fields: fields})
		}
		if len(msgs) == maxItems {
			v.Cursor = msgs[len(msgs)-1].ID
		}
	}
	// Long values in collections are cut too, so a page stays small.
	for i := range v.Items {
		if len(v.Items[i].Value) > maxCell {
			v.Items[i].Value, v.Cut = v.Items[i].Value[:maxCell], true
		}
	}
	return v, nil
}

// KeyEdit is a change to one key.
type KeyEdit struct {
	KeyRef
	// Op is what to do: set (a string), delete, rename (to Value),
	// expire (TTL milliseconds; 0 removes it), hset and hdel (Field),
	// rpush, lset (Index), lrem (Value), sadd, srem, zadd (Score), zrem,
	// and json.set (a JSON document at its root).
	Op    string   `json:"op"`
	Field string   `json:"field,omitempty"`
	Value string   `json:"value,omitempty"`
	Index int64    `json:"index,omitempty"`
	Score *float64 `json:"score,omitempty"`
	TTL   int64    `json:"ttl,omitempty"`
}

// EditKey changes one key and returns how many things it changed.
func (s *Service) EditKey(ctx context.Context, serverID, id string, e KeyEdit) (int64, error) {
	key, err := e.name()
	if err != nil {
		return 0, err
	}
	if len(e.Value) > maxEditValue || len(e.Field) > maxEditValue {
		return 0, invalid{"a value written from the browser is at most 1 MiB"}
	}
	c, err := s.redisAs(ctx, serverID, id)
	if err != nil {
		return 0, err
	}
	defer func() { _ = c.Close() }()
	switch e.Op {
	case "set":
		// Keeping its time to live, if it has one.
		return 1, c.SetArgs(ctx, key, e.Value, redis.SetArgs{KeepTTL: true}).Err()
	case "json.set":
		return 1, c.Do(ctx, "JSON.SET", key, "$", e.Value).Err()
	case "delete":
		return c.Unlink(ctx, key).Result()
	case "rename":
		if e.Value == "" {
			return 0, invalid{"give the key a new name"}
		}
		ok, err := c.RenameNX(ctx, key, e.Value).Result()
		if err == nil && !ok {
			return 0, invalid{fmt.Sprintf("there's already a key %q", e.Value)}
		}
		return 1, err
	case "expire":
		if e.TTL <= 0 {
			ok, err := c.Persist(ctx, key).Result()
			return b2i(ok), err
		}
		ok, err := c.PExpire(ctx, key, time.Duration(e.TTL)*time.Millisecond).Result()
		return b2i(ok), err
	case "hset":
		if _, err := c.HSet(ctx, key, e.Field, e.Value).Result(); err != nil {
			return 0, err
		}
		return 1, nil
	case "hdel":
		return c.HDel(ctx, key, e.Field).Result()
	case "rpush":
		_, err := c.RPush(ctx, key, e.Value).Result()
		return 1, err
	case "lset":
		return 1, c.LSet(ctx, key, e.Index, e.Value).Err()
	case "lrem":
		return c.LRem(ctx, key, 1, e.Value).Result()
	case "sadd":
		return c.SAdd(ctx, key, e.Value).Result()
	case "srem":
		return c.SRem(ctx, key, e.Value).Result()
	case "zadd":
		if e.Score == nil || math.IsNaN(*e.Score) {
			return 0, invalid{"give it a score"}
		}
		if _, err := c.ZAdd(ctx, key, redis.Z{Score: *e.Score, Member: e.Value}).Result(); err != nil {
			return 0, err
		}
		return 1, nil
	case "zrem":
		return c.ZRem(ctx, key, e.Value).Result()
	}
	return 0, invalid{fmt.Sprintf("unknown change %q", e.Op)}
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// CommandResult is a Redis command's reply.
type CommandResult struct {
	// Reply is the reply as JSON: strings, numbers, nulls, and arrays and
	// objects of them.
	Reply  any   `json:"reply"`
	Cut    bool  `json:"cut,omitempty"`
	Millis int64 `json:"millis"`
}

// RunCommand runs one Redis command as the database's user. Those who
// may only look run only what Redis itself flags read-only. Commands that
// block or subscribe aren't run: their connection would stay waiting.
func (s *Service) RunCommand(ctx context.Context, serverID, id string, args []string, readOnly bool) (CommandResult, error) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return CommandResult{}, invalid{"type a command"}
	}
	if len(args) > maxArgs {
		return CommandResult{}, invalid{fmt.Sprintf("a command has at most %d arguments here", maxArgs)}
	}
	c, err := s.redisAs(ctx, serverID, id)
	if err != nil {
		return CommandResult{}, err
	}
	defer func() { _ = c.Close() }()
	name := strings.ToLower(args[0])
	info, err := c.Command(ctx).Result()
	if err != nil {
		return CommandResult{}, err
	}
	ci := info[name]
	if ci == nil {
		return CommandResult{}, invalid{fmt.Sprintf("Redis has no command %q", args[0])}
	}
	flags := ci.Flags
	if slices.Contains(flags, "blocking") || slices.Contains(flags, "pubsub") ||
		slices.Contains([]string{"monitor", "subscribe", "psubscribe", "ssubscribe", "sync", "psync", "multi", "exec", "watch", "quit", "reset", "hello", "auth", "select"}, name) {
		return CommandResult{}, invalid{fmt.Sprintf("%s isn't run from the browser: it would hold its connection", strings.ToUpper(name))}
	}
	if readOnly && (!slices.Contains(flags, "readonly") || slices.Contains(flags, "write")) {
		return CommandResult{}, invalid{fmt.Sprintf("you can only look: %s isn't a read-only command", strings.ToUpper(name))}
	}
	cargs := make([]any, len(args))
	for i, a := range args {
		cargs[i] = a
	}
	start := time.Now()
	reply, err := c.Do(ctx, cargs...).Result()
	res := CommandResult{Millis: time.Since(start).Milliseconds()}
	if errors.Is(err, redis.Nil) {
		return res, nil
	}
	if err != nil {
		return res, err
	}
	n := 0
	res.Reply = jsonReply(reply, &n, &res.Cut)
	return res, nil
}

// jsonReply turns a reply into JSON's types, cutting it short past
// maxReplyKeys elements.
func jsonReply(v any, n *int, cut *bool) any {
	*n++
	if *n > maxReplyKeys {
		*cut = true
		return nil
	}
	switch t := v.(type) {
	case string:
		if len(t) > maxCell {
			*cut = true
			t = t[:maxCell]
		}
		if !utf8.ValidString(t) {
			return "0x" + hex.EncodeToString([]byte(t))
		}
		return t
	case []any:
		out := make([]any, 0, len(t))
		for _, x := range t {
			if *n > maxReplyKeys {
				*cut = true
				break
			}
			out = append(out, jsonReply(x, n, cut))
		}
		return out
	case map[any]any:
		out := map[string]any{}
		for k, x := range t {
			out[fmt.Sprint(k)] = jsonReply(x, n, cut)
		}
		return out
	case error:
		return "(error) " + t.Error()
	}
	return v // int64, float64, bool, nil
}
