package cloudflarekv

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/lnxjedi/gopherbot/robot"
)

const brainCacheFormat = "gopherbot-brain-v3"

type cfKVBrainConfig struct {
	AccountID                   string
	NamespaceID                 string
	APIToken                    string
	CloudWriteBudgetPerDay      int
	CloudWriteMinIntervalMillis int
	CoalesceWindowMillis        int
	FlushOnShutdownMaxMillis    int
	CheckpointVerifyRetries     int
	CheckpointVerifyDelayMillis int
}

type cfKVRemoteBrain struct {
	cfg     cfKVBrainConfig
	handler robot.Handler
	client  *http.Client
}

type cfKVEnvelope struct {
	Format    string    `json:"format"`
	Payload   string    `json:"payload,omitempty"`
	Version   uint64    `json:"version"`
	Checksum  string    `json:"checksum"`
	Deleted   bool      `json:"deleted"`
	UpdatedAt time.Time `json:"updated_at"`
}

func remoteProvider(r robot.Handler) robot.RemoteBrainBackend {
	var cfg cfKVBrainConfig
	if err := r.GetBrainConfig(&cfg); err != nil {
		r.Log(robot.Fatal, "Unable to retrieve Cloudflare KV brain configuration: %v", err)
	}
	if cfg.CloudWriteBudgetPerDay <= 0 {
		cfg.CloudWriteBudgetPerDay = 900
	}
	if cfg.CloudWriteMinIntervalMillis <= 0 {
		cfg.CloudWriteMinIntervalMillis = 1100
	}
	if cfg.CoalesceWindowMillis <= 0 {
		cfg.CoalesceWindowMillis = 2000
	}
	if cfg.FlushOnShutdownMaxMillis <= 0 {
		cfg.FlushOnShutdownMaxMillis = 10000
	}
	if cfg.CheckpointVerifyRetries <= 0 {
		cfg.CheckpointVerifyRetries = 5
	}
	if cfg.CheckpointVerifyDelayMillis <= 0 {
		cfg.CheckpointVerifyDelayMillis = 2000
	}
	b := &cfKVRemoteBrain{
		cfg:     cfg,
		handler: r,
		client:  http.DefaultClient,
	}
	r.Log(robot.Info, "Initialized Cloudflare KV remote brain namespace '%s'", cfg.NamespaceID)
	return b
}

func (b *cfKVRemoteBrain) Identity() robot.BrainBackendIdentity {
	return robot.BrainBackendIdentity{
		Provider: "cloudflare",
		Scope:    b.cfg.AccountID + "/" + b.cfg.NamespaceID,
	}
}

func (b *cfKVRemoteBrain) SyncPolicy() robot.BrainSyncPolicy {
	return robot.BrainSyncPolicy{
		WriteBudgetPerDay:          b.cfg.CloudWriteBudgetPerDay,
		MinWriteInterval:           time.Duration(b.cfg.CloudWriteMinIntervalMillis) * time.Millisecond,
		CoalesceWindow:             time.Duration(b.cfg.CoalesceWindowMillis) * time.Millisecond,
		FlushOnShutdownMaxDuration: time.Duration(b.cfg.FlushOnShutdownMaxMillis) * time.Millisecond,
		CheckpointVerifyRetries:    b.cfg.CheckpointVerifyRetries,
		CheckpointVerifyDelay:      time.Duration(b.cfg.CheckpointVerifyDelayMillis) * time.Millisecond,
	}
}

func (b *cfKVRemoteBrain) Get(ctx context.Context, key string) (robot.RemoteBrainRecord, bool, error) {
	data, exists, err := b.fetch(ctx, key)
	if err != nil || !exists {
		return robot.RemoteBrainRecord{}, exists, err
	}
	record, err := decodeEnvelope(key, data)
	if err != nil {
		return robot.RemoteBrainRecord{Key: key}, true, err
	}
	return record, true, nil
}

func (b *cfKVRemoteBrain) Put(ctx context.Context, record robot.RemoteBrainRecord) error {
	payload, err := encodeEnvelope(record)
	if err != nil {
		return err
	}
	return b.putRaw(ctx, record.Key, payload)
}

func (b *cfKVRemoteBrain) Delete(ctx context.Context, tombstone robot.RemoteBrainRecord) error {
	tombstone.Format = brainCacheFormat
	tombstone.Deleted = true
	payload, err := encodeEnvelope(tombstone)
	if err != nil {
		return err
	}
	return b.putRaw(ctx, tombstone.Key, payload)
}

func (b *cfKVRemoteBrain) ListMetadata(ctx context.Context, cursor string, limit int) (robot.RemoteBrainPage, error) {
	keys, next, err := b.listKeys(ctx, cursor, limit)
	if err != nil {
		return robot.RemoteBrainPage{}, err
	}
	records := make([]robot.RemoteBrainRecord, 0, len(keys))
	for _, key := range keys {
		record, exists, err := b.Get(ctx, key)
		if err != nil {
			records = append(records, robot.RemoteBrainRecord{Key: key})
			continue
		}
		if exists {
			record.Payload = nil
			records = append(records, record)
		}
	}
	return robot.RemoteBrainPage{Records: records, NextCursor: next}, nil
}

func (b *cfKVRemoteBrain) Shutdown() {}

func (b *cfKVRemoteBrain) putRaw(ctx context.Context, key string, data []byte) error {
	endpoint := fmt.Sprintf(
		"https://api.cloudflare.com/client/v4/accounts/%s/storage/kv/namespaces/%s/values/%s",
		b.cfg.AccountID, b.cfg.NamespaceID, url.PathEscape(key),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+b.cfg.APIToken)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			b.handler.Log(robot.Fatal, "CF KV store: invalid token? body=%s", string(body))
		}
		return fmt.Errorf("CF KV store error: status %d, body=%s", resp.StatusCode, string(body))
	}
	return nil
}

func (b *cfKVRemoteBrain) fetch(ctx context.Context, key string) ([]byte, bool, error) {
	endpoint := fmt.Sprintf(
		"https://api.cloudflare.com/client/v4/accounts/%s/storage/kv/namespaces/%s/values/%s",
		b.cfg.AccountID, b.cfg.NamespaceID, url.PathEscape(key),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Authorization", "Bearer "+b.cfg.APIToken)
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		body, err := io.ReadAll(resp.Body)
		return body, true, err
	case http.StatusNotFound:
		return nil, false, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		body, _ := io.ReadAll(resp.Body)
		b.handler.Log(robot.Fatal, "CF KV fetch: invalid token? body=%s", string(body))
		return nil, false, fmt.Errorf("CF KV fetch fatal: unauthorized/forbidden")
	default:
		body, _ := io.ReadAll(resp.Body)
		return nil, false, fmt.Errorf("CF KV fetch error: status %d, body=%s", resp.StatusCode, string(body))
	}
}

func (b *cfKVRemoteBrain) listKeys(ctx context.Context, cursor string, limit int) ([]string, string, error) {
	if limit <= 0 {
		limit = 1000
	}
	endpoint := fmt.Sprintf(
		"https://api.cloudflare.com/client/v4/accounts/%s/storage/kv/namespaces/%s/keys?limit=%d",
		b.cfg.AccountID, b.cfg.NamespaceID, limit,
	)
	if cursor != "" {
		endpoint += "&cursor=" + url.QueryEscape(cursor)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+b.cfg.APIToken)
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			b.handler.Log(robot.Fatal, "CF KV list: invalid token? body=%s", string(body))
		}
		return nil, "", fmt.Errorf("CF KV list error: status %d, body=%s", resp.StatusCode, string(body))
	}
	var listResp struct {
		Result []struct {
			Name string `json:"name"`
		} `json:"result"`
		ResultInfo struct {
			Cursor string `json:"cursor"`
		} `json:"result_info"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, "", err
	}
	keys := make([]string, 0, len(listResp.Result))
	for _, result := range listResp.Result {
		keys = append(keys, result.Name)
	}
	return keys, listResp.ResultInfo.Cursor, nil
}

func encodeEnvelope(record robot.RemoteBrainRecord) ([]byte, error) {
	env := cfKVEnvelope{
		Format:    brainCacheFormat,
		Payload:   base64.StdEncoding.EncodeToString(record.Payload),
		Version:   record.Version,
		Checksum:  record.Checksum,
		Deleted:   record.Deleted,
		UpdatedAt: record.UpdatedAt,
	}
	if env.UpdatedAt.IsZero() {
		env.UpdatedAt = time.Now().UTC()
	}
	return json.Marshal(env)
}

func decodeEnvelope(key string, data []byte) (robot.RemoteBrainRecord, error) {
	var env cfKVEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return robot.RemoteBrainRecord{Key: key}, err
	}
	if env.Format != brainCacheFormat {
		return robot.RemoteBrainRecord{Key: key}, fmt.Errorf("not a v3 brain record")
	}
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		return robot.RemoteBrainRecord{Key: key}, err
	}
	return robot.RemoteBrainRecord{
		Key:       key,
		Payload:   payload,
		Format:    env.Format,
		Version:   env.Version,
		Checksum:  env.Checksum,
		Deleted:   env.Deleted,
		UpdatedAt: env.UpdatedAt,
	}, nil
}

func (b *cfKVRemoteBrain) ListV2(ctx context.Context, cursor string, limit int) (robot.LegacyBrainPage, error) {
	keys, next, err := b.listKeys(ctx, cursor, limit)
	if err != nil {
		return robot.LegacyBrainPage{}, err
	}
	records := make([]robot.LegacyBrainRecord, 0, len(keys))
	for _, key := range keys {
		records = append(records, robot.LegacyBrainRecord{Key: key})
	}
	return robot.LegacyBrainPage{Records: records, NextCursor: next}, nil
}

func (b *cfKVRemoteBrain) GetV2(ctx context.Context, key string) (robot.LegacyBrainRecord, bool, error) {
	payload, exists, err := b.fetch(ctx, key)
	if err != nil || !exists {
		return robot.LegacyBrainRecord{}, exists, err
	}
	return robot.LegacyBrainRecord{Key: key, Payload: payload}, true, nil
}

func (b *cfKVRemoteBrain) PutV2(ctx context.Context, record robot.LegacyBrainRecord) error {
	return b.putRaw(ctx, record.Key, record.Payload)
}

func (b *cfKVRemoteBrain) DeleteV2(ctx context.Context, key string) error {
	endpoint := fmt.Sprintf(
		"https://api.cloudflare.com/client/v4/accounts/%s/storage/kv/namespaces/%s/values/%s",
		b.cfg.AccountID, b.cfg.NamespaceID, url.PathEscape(key),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+b.cfg.APIToken)
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("CF KV delete error: status %d, body=%s", resp.StatusCode, string(body))
	}
	return nil
}
