package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithy "github.com/aws/smithy-go"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
)

// ConnectionCheck is the outcome of one step of a settings connection test.
type ConnectionCheck struct {
	Step   string `json:"step"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// ConnectionResult is what the admin settings page shows after a test.
type ConnectionResult struct {
	OK        bool              `json:"ok"`
	Checks    []ConnectionCheck `json:"checks"`
	ElapsedMS int64             `json:"elapsed_ms"`
}

func (r *ConnectionResult) add(step string, err error) bool {
	c := ConnectionCheck{Step: step, OK: err == nil}
	if err != nil {
		c.Detail = err.Error()
	}
	r.Checks = append(r.Checks, c)
	return err == nil
}

// MergeS3 fills empty fields of draft from saved, so a test can run on the
// form as typed while secrets left blank keep their stored value.
func MergeS3(draft, saved config.S3Config) config.S3Config {
	if draft.Endpoint == "" {
		draft.Endpoint = saved.Endpoint
	}
	if draft.Bucket == "" {
		draft.Bucket = saved.Bucket
	}
	if draft.AccessKey == "" {
		draft.AccessKey = saved.AccessKey
	}
	if draft.SecretKey == "" {
		draft.SecretKey = saved.SecretKey
	}
	if draft.Region == "" {
		draft.Region = saved.Region
	}
	return draft
}

// CheckS3Connection proves the bucket is usable for backups: it writes a small probe
// object, reads it back, and deletes it. Nothing else in the bucket is read.
func CheckS3Connection(ctx context.Context, cfg config.S3Config) (res ConnectionResult) {
	start := time.Now()
	defer func() { res.ElapsedMS = time.Since(start).Milliseconds() }()

	var missing []string
	for name, v := range map[string]string{"endpoint": cfg.Endpoint, "bucket": cfg.Bucket, "access_key": cfg.AccessKey, "secret_key": cfg.SecretKey} {
		if strings.TrimSpace(v) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		res.add("config", fmt.Errorf("missing: %s", strings.Join(missing, ", ")))
		return res
	}
	if cfg.Region == "" {
		cfg.Region = "auto"
	}
	res.add("config", nil)

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client := newS3Client(cfg)
	key := fmt.Sprintf("proxima-connection-test/%d.txt", time.Now().UnixNano())
	payload := []byte("proxima backup connection test")

	_, err := client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(cfg.Bucket), Key: aws.String(key), Body: bytes.NewReader(payload)})
	if !res.add("write", describeS3Error(err)) {
		return res
	}
	out, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(cfg.Bucket), Key: aws.String(key)})
	if err == nil {
		got, readErr := io.ReadAll(out.Body)
		_ = out.Body.Close()
		if readErr == nil && !bytes.Equal(got, payload) {
			readErr = errors.New("read back different content")
		}
		err = readErr
	}
	readOK := res.add("read", describeS3Error(err))
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(cfg.Bucket), Key: aws.String(key)})
	deleteOK := res.add("delete", describeS3Error(err))
	res.OK = readOK && deleteOK
	return res
}

func describeS3Error(err error) error {
	if err == nil {
		return nil
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return fmt.Errorf("%s: %s", apiErr.ErrorCode(), apiErr.ErrorMessage())
	}
	return err
}

// CheckTelegramConnection checks the bot token with getMe, then sends one test message
// to the chat. telegramAPI is the API base URL; tests point it at a stub.
func CheckTelegramConnection(ctx context.Context, telegramAPI, token, chatID string) (res ConnectionResult) {
	start := time.Now()
	defer func() { res.ElapsedMS = time.Since(start).Milliseconds() }()

	if strings.TrimSpace(token) == "" || strings.TrimSpace(chatID) == "" {
		res.add("config", errors.New("bot token and chat ID are both required"))
		return res
	}
	res.add("config", nil)

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client := &http.Client{}

	var me struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			Username string `json:"username"`
		} `json:"result"`
	}
	if err := telegramCall(ctx, client, telegramAPI, token, "getMe", nil, &me); err != nil {
		res.add("token", err)
		return res
	}
	res.Checks = append(res.Checks, ConnectionCheck{Step: "token", OK: true, Detail: "@" + me.Result.Username})

	var sent struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	msg := map[string]string{"chat_id": chatID, "text": "✅ Proxima 알림 연결 테스트 / connection test"}
	res.OK = res.add("send", telegramCall(ctx, client, telegramAPI, token, "sendMessage", msg, &sent))
	return res
}

func telegramCall(ctx context.Context, client *http.Client, base, token, method string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/bot%s/%s", strings.TrimRight(base, "/"), token, method), reader)
	if err != nil {
		return errors.New("the bot token contains characters Telegram does not accept")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		// The URL embeds the bot token; never surface it in the error.
		return errors.New("could not reach the Telegram API")
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var envelope struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(raw, &envelope)
	if resp.StatusCode != http.StatusOK || !envelope.OK {
		if envelope.Description != "" {
			return errors.New(envelope.Description)
		}
		return fmt.Errorf("Telegram returned HTTP %d", resp.StatusCode)
	}
	return json.Unmarshal(raw, out)
}
