//go:build bdd

package bdd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type client struct {
	api    string
	mpfake string
	token  string
	http   *http.Client
}

func newClient() *client {
	return &client{
		api:    envOr("BDD_API_URL", "http://localhost:8080/api"),
		mpfake: envOr("BDD_MPFAKE_URL", "http://localhost:8095"),
		http:   &http.Client{Timeout: 15 * time.Second},
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (c *client) do(ctx context.Context, method, url string, body any, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, fmt.Errorf("%s %s: %d %s", method, url, resp.StatusCode, raw)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("%s %s: decode %s: %w", method, url, raw, err)
		}
	}
	return resp.StatusCode, nil
}

func (c *client) call(ctx context.Context, method, path string, body, out any) error {
	_, err := c.do(ctx, method, c.api+path, body, out)
	return err
}

func (c *client) login(ctx context.Context) error {
	creds := map[string]string{"username": "bdd.operador", "password": "Bdd@Senha12345"}
	register := map[string]string{"username": creds["username"], "password": creds["password"], "role": "admin"}
	if status, err := c.do(ctx, http.MethodPost, c.api+"/auth/register", register, nil); err != nil && status != http.StatusConflict {
		return err
	}

	var out struct {
		Token string `json:"token"`
	}
	if err := c.call(ctx, http.MethodPost, "/auth/login", creds, &out); err != nil {
		return err
	}
	c.token = out.Token
	return nil
}

func eventually(ctx context.Context, timeout time.Duration, check func() (bool, string, error)) error {
	deadline := time.Now().Add(timeout)
	last := ""
	for {
		ok, observed, err := check()
		if err == nil && ok {
			return nil
		}
		if err != nil {
			last = err.Error()
		} else {
			last = observed
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("condicao nao atingida em %s; ultimo valor observado: %s", timeout, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
