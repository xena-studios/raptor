// Package storage runs Raptor Backup Storage for the Panel
// (docs/PANEL.md#raptor-backup-storage): a Backblaze B2 key per node,
// limited to its folder in Raptor's bucket; each org's usage, measured
// daily; and deleting a node's data 30 days after it's turned off.
package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// B2 talks to Backblaze B2's own API (v3), with a key that can make keys
// and list and delete files in the bucket. Only what Raptor needs.
type B2 struct {
	KeyID  string
	Key    string
	Client *http.Client
	// AuthURL is B2's (tests point it elsewhere).
	AuthURL string
}

const b2AuthURL = "https://api.backblazeb2.com/b2api/v3/b2_authorize_account"

// b2Session is an authorized account: valid for a day, used for minutes.
type b2Session struct {
	accountID string
	token     string
	apiURL    string
}

// b2Error is an error B2 answered with.
type b2Error struct {
	Status  int    `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *b2Error) Error() string { return fmt.Sprintf("b2: %d %s: %s", e.Status, e.Code, e.Message) }

func (b *B2) client() *http.Client {
	if b.Client != nil {
		return b.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (b *B2) authorize(ctx context.Context) (*b2Session, error) {
	u := b.AuthURL
	if u == "" {
		u = b2AuthURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(b.KeyID, b.Key)
	var out struct {
		AccountID string `json:"accountId"`
		Token     string `json:"authorizationToken"`
		APIInfo   struct {
			StorageAPI struct {
				APIURL string `json:"apiUrl"`
			} `json:"storageApi"`
		} `json:"apiInfo"`
	}
	if err := b.do(req, &out); err != nil {
		return nil, err
	}
	return &b2Session{accountID: out.AccountID, token: out.Token, apiURL: out.APIInfo.StorageAPI.APIURL}, nil
}

func (b *B2) call(ctx context.Context, s *b2Session, name string, body, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.apiURL+"/b2api/v3/"+name, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", s.token)
	req.Header.Set("Content-Type", "application/json")
	return b.do(req, out)
}

func (b *B2) do(req *http.Request, out any) error {
	res, err := b.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		e := &b2Error{Status: res.StatusCode}
		_ = json.Unmarshal(body, e)
		return e
	}
	return json.Unmarshal(body, out)
}

// Key is a B2 application key: the ID, and the secret, shown once.
type Key struct {
	ID     string
	Secret string
}

// keyCapabilities let a node use its folder through the S3 API, and
// nothing else: no other keys, no bucket settings.
var keyCapabilities = []string{"listBuckets", "listFiles", "readFiles", "writeFiles", "deleteFiles"}

// CreateKey makes a key for one folder of one bucket.
func (b *B2) CreateKey(ctx context.Context, name, bucketID, prefix string) (Key, error) {
	s, err := b.authorize(ctx)
	if err != nil {
		return Key{}, err
	}
	var out struct {
		ID     string `json:"applicationKeyId"`
		Secret string `json:"applicationKey"`
	}
	err = b.call(ctx, s, "b2_create_key", map[string]any{
		"accountId": s.accountID, "keyName": name, "capabilities": keyCapabilities,
		"bucketId": bucketID, "namePrefix": prefix,
	}, &out)
	return Key{ID: out.ID, Secret: out.Secret}, err
}

// DeleteKey deletes a key. One that's already gone is fine.
func (b *B2) DeleteKey(ctx context.Context, id string) error {
	s, err := b.authorize(ctx)
	if err != nil {
		return err
	}
	err = b.call(ctx, s, "b2_delete_key", map[string]any{"applicationKeyId": id}, &struct{}{})
	var be *b2Error
	if errors.As(err, &be) && (be.Code == "bad_request" || be.Status == http.StatusNotFound) {
		return nil
	}
	return err
}

type b2File struct {
	Name   string `json:"fileName"`
	ID     string `json:"fileId"`
	Size   int64  `json:"contentLength"`
	Action string `json:"action"`
}

// pageSize is the most B2 lists in one call (a paid one past 1,000).
const pageSize = 10000

// Usage adds up what's stored under a prefix: every version B2 keeps,
// since every one is billed.
func (b *B2) Usage(ctx context.Context, bucketID, prefix string) (int64, error) {
	var total int64
	err := b.versions(ctx, bucketID, prefix, func(f b2File) error {
		if f.Action == "upload" {
			total += f.Size
		}
		return nil
	})
	return total, err
}

// DeletePrefix deletes every version of every file under a prefix.
func (b *B2) DeletePrefix(ctx context.Context, bucketID, prefix string) (int, error) {
	s, err := b.authorize(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	err = b.versions(ctx, bucketID, prefix, func(f b2File) error {
		if err := b.call(ctx, s, "b2_delete_file_version", map[string]any{"fileName": f.Name, "fileId": f.ID}, &struct{}{}); err != nil {
			return err
		}
		n++
		return nil
	})
	return n, err
}

func (b *B2) versions(ctx context.Context, bucketID, prefix string, fn func(b2File) error) error {
	if prefix == "" {
		return errors.New("refusing to list the whole bucket")
	}
	s, err := b.authorize(ctx)
	if err != nil {
		return err
	}
	var nextName, nextID string
	for {
		body := map[string]any{"bucketId": bucketID, "prefix": prefix, "maxFileCount": pageSize}
		if nextName != "" {
			body["startFileName"], body["startFileId"] = nextName, nextID
		}
		var page struct {
			Files    []b2File `json:"files"`
			NextName *string  `json:"nextFileName"`
			NextID   *string  `json:"nextFileId"`
		}
		if err := b.call(ctx, s, "b2_list_file_versions", body, &page); err != nil {
			return err
		}
		for _, f := range page.Files {
			if err := fn(f); err != nil {
				return err
			}
		}
		if page.NextName == nil {
			return nil
		}
		nextName, nextID = *page.NextName, ""
		if page.NextID != nil {
			nextID = *page.NextID
		}
	}
}
