package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// feishuDefaultBase is the public Feishu/Lark open-platform endpoint.
const feishuDefaultBase = "https://open.feishu.cn"

// FeishuClient performs the Feishu OAuth authorization-code flow.
type FeishuClient struct {
	AppID       string
	AppSecret   string
	RedirectURL string
	BaseURL     string // overridable for tests; defaults to feishuDefaultBase
	HTTP        *http.Client
}

// FeishuUser is the subset of profile fields loadify maps to a local user.
type FeishuUser struct {
	OpenID string
	Name   string
	Email  string
}

// Enabled reports whether Feishu login is configured.
func (c *FeishuClient) Enabled() bool { return c != nil && c.AppID != "" && c.AppSecret != "" }

func (c *FeishuClient) base() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return feishuDefaultBase
}

func (c *FeishuClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// AuthCodeURL builds the Feishu authorization URL the browser is redirected to.
func (c *FeishuClient) AuthCodeURL(state string) string {
	q := url.Values{}
	q.Set("app_id", c.AppID)
	q.Set("redirect_uri", c.RedirectURL)
	q.Set("state", state)
	return c.base() + "/open-apis/authen/v1/index?" + q.Encode()
}

// Exchange swaps an authorization code for the user's profile.
func (c *FeishuClient) Exchange(ctx context.Context, code string) (*FeishuUser, error) {
	appToken, err := c.appAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	userToken, err := c.userAccessToken(ctx, appToken, code)
	if err != nil {
		return nil, err
	}
	return c.userInfo(ctx, userToken)
}

func (c *FeishuClient) appAccessToken(ctx context.Context) (string, error) {
	body, _ := json.Marshal(map[string]string{"app_id": c.AppID, "app_secret": c.AppSecret})
	var out struct {
		Code     int    `json:"code"`
		Msg      string `json:"msg"`
		AppToken string `json:"app_access_token"`
	}
	if err := c.post(ctx, "/open-apis/auth/v3/app_access_token/internal", "", body, &out); err != nil {
		return "", err
	}
	if out.Code != 0 || out.AppToken == "" {
		return "", fmt.Errorf("feishu: app_access_token failed: code=%d msg=%s", out.Code, out.Msg)
	}
	return out.AppToken, nil
}

func (c *FeishuClient) userAccessToken(ctx context.Context, appToken, code string) (string, error) {
	body, _ := json.Marshal(map[string]string{"grant_type": "authorization_code", "code": code})
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if err := c.post(ctx, "/open-apis/authen/v1/oidc/access_token", appToken, body, &out); err != nil {
		return "", err
	}
	if out.Code != 0 || out.Data.AccessToken == "" {
		return "", fmt.Errorf("feishu: user access_token failed: code=%d msg=%s", out.Code, out.Msg)
	}
	return out.Data.AccessToken, nil
}

func (c *FeishuClient) userInfo(ctx context.Context, userToken string) (*FeishuUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/open-apis/authen/v1/user_info", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+userToken)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("feishu: user_info: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			OpenID string `json:"open_id"`
			Name   string `json:"name"`
			Email  string `json:"email"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("feishu: decode user_info: %w", err)
	}
	if out.Code != 0 || out.Data.OpenID == "" {
		return nil, fmt.Errorf("feishu: user_info failed: code=%d msg=%s", out.Code, out.Msg)
	}
	return &FeishuUser{OpenID: out.Data.OpenID, Name: out.Data.Name, Email: out.Data.Email}, nil
}

// post issues a JSON POST, optionally bearer-authenticated, decoding into out.
func (c *FeishuClient) post(ctx context.Context, path, bearer string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("feishu: %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("feishu: decode %s: %w", path, err)
	}
	return nil
}
