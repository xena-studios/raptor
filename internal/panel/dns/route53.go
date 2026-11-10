package dns

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Route53 is a Provider for an Amazon Route 53 hosted zone, through its API
// with an IAM user's access key. The key's policy (docs/DEPLOY.md#raptornodesnet)
// lets it change only node records: A and AAAA, never the apex, www, or the
// wildcard that answers for names with no node.
type Route53 struct {
	AccessKeyID     string
	SecretAccessKey string
	ZoneID          string
	// Endpoint defaults to https://route53.amazonaws.com.
	Endpoint string
	Client   *http.Client
	// now is the signing time (tests).
	now func() time.Time
}

const route53NS = "https://route53.amazonaws.com/doc/2013-04-01/"

type r53Record struct {
	Value string `xml:"Value"`
}

type r53RecordSet struct {
	Name            string      `xml:"Name"`
	Type            string      `xml:"Type"`
	TTL             int         `xml:"TTL,omitempty"`
	ResourceRecords []r53Record `xml:"ResourceRecords>ResourceRecord,omitempty"`
	// Set on alias records (the apex and the wildcard): never ours.
	AliasTarget *struct {
		DNSName string `xml:"DNSName"`
	} `xml:"AliasTarget,omitempty"`
}

type r53Change struct {
	Action string       `xml:"Action"`
	Set    r53RecordSet `xml:"ResourceRecordSet"`
}

type r53ChangeRequest struct {
	XMLName xml.Name    `xml:"ChangeResourceRecordSetsRequest"`
	Xmlns   string      `xml:"xmlns,attr"`
	Comment string      `xml:"ChangeBatch>Comment"`
	Changes []r53Change `xml:"ChangeBatch>Changes>Change"`
}

type r53ListResponse struct {
	Sets []r53RecordSet `xml:"ResourceRecordSets>ResourceRecordSet"`
}

type r53Error struct {
	Code    string `xml:"Error>Code"`
	Message string `xml:"Error>Message"`
}

// fqdn is name as Route 53 writes it: lower case, with the final dot.
func fqdn(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, ".")) + "."
}

// Set implements Provider.
func (r *Route53) Set(ctx context.Context, name, typ string, addr netip.Addr) error {
	if typ != "A" && typ != "AAAA" {
		return errors.New("record type must be A or AAAA")
	}
	if !addr.IsValid() {
		return r.Remove(ctx, name, typ)
	}
	// UPSERT replaces whatever the name has of this type, so there are
	// never duplicates to clean up.
	return r.change(ctx, r53Change{Action: "UPSERT", Set: r53RecordSet{
		Name: fqdn(name), Type: typ, TTL: TTL,
		ResourceRecords: []r53Record{{Value: addr.String()}},
	}})
}

// Remove deletes name's records of typ (A, AAAA, CNAME, or SRV). Route 53
// deletes only an exact copy of the record set, so it's read first; none is
// nothing to do.
func (r *Route53) Remove(ctx context.Context, name, typ string) error {
	switch typ {
	case "A", "AAAA", "CNAME", "SRV":
	default:
		return fmt.Errorf("record type %q", typ)
	}
	set, err := r.get(ctx, fqdn(name), typ)
	if err != nil || set == nil {
		return err
	}
	if set.AliasTarget != nil {
		return fmt.Errorf("route53: %s %s is an alias record, not a node's", name, typ)
	}
	err = r.change(ctx, r53Change{Action: "DELETE", Set: *set})
	var ae *apiError
	if errors.As(err, &ae) && ae.Code == "InvalidChangeBatch" && strings.Contains(ae.Message, "not found") {
		return nil // deleted meanwhile
	}
	return err
}

// get returns the record set of name and typ, or nil if there's none.
func (r *Route53) get(ctx context.Context, name, typ string) (*r53RecordSet, error) {
	q := url.Values{"name": {name}, "type": {typ}, "maxitems": {"1"}}
	var out r53ListResponse
	if err := r.call(ctx, http.MethodGet, "/rrset", q, nil, &out); err != nil {
		return nil, err
	}
	// The list starts at name and typ, or at the next set after them.
	for _, s := range out.Sets {
		if strings.EqualFold(s.Name, name) && s.Type == typ {
			return &s, nil
		}
	}
	return nil, nil
}

func (r *Route53) change(ctx context.Context, c r53Change) error {
	body, err := xml.Marshal(r53ChangeRequest{Xmlns: route53NS, Comment: "Raptor node", Changes: []r53Change{c}})
	if err != nil {
		return err
	}
	return r.call(ctx, http.MethodPost, "/rrset/", nil, append([]byte(xml.Header), body...), nil)
}

type apiError struct {
	Status        string
	Code, Message string
}

func (e *apiError) Error() string {
	if e.Code == "" {
		return "route53: " + e.Status
	}
	return fmt.Sprintf("route53: %s: %s", e.Code, e.Message)
}

// call sends a request to the zone's path (/rrset...), signed.
func (r *Route53) call(ctx context.Context, method, path string, q url.Values, body []byte, out any) error {
	endpoint := r.Endpoint
	if endpoint == "" {
		endpoint = "https://route53.amazonaws.com"
	}
	u := endpoint + "/2013-04-01/hostedzone/" + url.PathEscape(strings.TrimPrefix(r.ZoneID, "/hostedzone/")) + path
	if len(q) > 0 {
		u += "?" + canonicalQuery(q)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "text/xml")
	}
	now := time.Now
	if r.now != nil {
		now = r.now
	}
	signV4(req, body, r.AccessKeyID, r.SecretAccessKey, "us-east-1", "route53", now())
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		e := &apiError{Status: resp.Status}
		var x r53Error
		if xml.Unmarshal(b, &x) == nil {
			e.Code, e.Message = x.Code, x.Message
		}
		return e
	}
	if out != nil {
		return xml.Unmarshal(b, out)
	}
	return nil
}

// uriEncode escapes s as SigV4 wants: everything but A-Z a-z 0-9 - _ . ~.
func uriEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vs := append([]string(nil), q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, uriEncode(k)+"="+uriEncode(v))
		}
	}
	return strings.Join(parts, "&")
}

func hmacSHA256(key []byte, s string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(s))
	return h.Sum(nil)
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// signV4 signs req with AWS Signature Version 4: the host, x-amz-date, and
// content-type (if set) headers and the body.
func signV4(req *http.Request, body []byte, keyID, secret, region, service string, t time.Time) {
	t = t.UTC()
	amzDate := t.Format("20060102T150405Z")
	day := t.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)

	headers := map[string]string{"host": req.URL.Host, "x-amz-date": amzDate}
	if ct := req.Header.Get("Content-Type"); ct != "" {
		headers["content-type"] = ct
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, k := range names {
		canonHeaders.WriteString(k + ":" + strings.TrimSpace(headers[k]) + "\n")
	}
	signed := strings.Join(names, ";")
	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	canonical := strings.Join([]string{
		req.Method, path, req.URL.RawQuery, canonHeaders.String(), signed, sha256Hex(body),
	}, "\n")
	scope := day + "/" + region + "/" + service + "/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonical))
	key := hmacSHA256([]byte("AWS4"+secret), day)
	key = hmacSHA256(key, region)
	key = hmacSHA256(key, service)
	key = hmacSHA256(key, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(key, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+keyID+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}
