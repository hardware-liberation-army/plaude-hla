package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

const platformBase = "https://platform.plaud.cn"

type TokenCache struct {
	APIToken  string  `json:"api_token"`
	ExpiresAt float64 `json:"expires_at"`
}

type SDKAuth struct {
	appKey    string
	appSecret string
	cachePath string
	mu        sync.Mutex
}

func NewSDKAuth(appKey, appSecret, cachePath string) *SDKAuth {
	return &SDKAuth{
		appKey:    appKey,
		appSecret: cachePath,
		cachePath: cachePath,
	}
}

func (a *SDKAuth) loadCache() (*TokenCache, error) {
	data, err := os.ReadFile(a.cachePath)
	if err != nil {
		return nil, err
	}
	var cache TokenCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}
	return &cache, nil
}

func (a *SDKAuth) saveCache(cache *TokenCache) error {
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	return os.WriteFile(a.cachePath, data, 0600)
}

func (a *SDKAuth) GetAPIToken() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	cache, err := a.loadCache()
	if err == nil && cache.APIToken != "" && time.Now().Unix() < int64(cache.ExpiresAt) {
		remaining := int64(cache.ExpiresAt) - time.Now().Unix()
		fmt.Printf("[SDK] Using cached api_token (expires in %ds)\n", remaining)
		return cache.APIToken, nil
	}

	fmt.Println("[SDK] Starting SDK token exchange...")

	sdkToken, err := a.getSdkToken()
	if err != nil {
		return "", fmt.Errorf("getting SDK token: %w", err)
	}

	apiToken, err := a.getAPIToken(sdkToken)
	if err != nil {
		return "", fmt.Errorf("getting API token: %w", err)
	}

	cache = &TokenCache{
		APIToken:  apiToken,
		ExpiresAt: float64(time.Now().Unix() + 30*24*3600),
	}
	if err := a.saveCache(cache); err != nil {
		fmt.Printf("[SDK] Warning: could not save token cache: %v\n", err)
	}

	return apiToken, nil
}

func (a *SDKAuth) getSdkToken() (string, error) {
	creds := a.appKey + ":" + a.appSecret
	b64 := base64.StdEncoding.EncodeToString([]byte(creds))

	req, _ := http.NewRequest("POST", platformBase+"/api/oauth/sdk-token", nil)
	req.Header.Set("Authorization", "Bearer "+b64)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("SDK token request failed: %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		SdkToken  string `json:"sdk_token"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parsing SDK token response: %w", err)
	}

	fmt.Printf("[SDK] Got sdk_token (expires in %ds)\n", result.ExpiresIn)
	return result.SdkToken, nil
}

func (a *SDKAuth) getAPIToken(sdkToken string) (string, error) {
	payload, _ := json.Marshal(nil)
	req, _ := http.NewRequest("POST", platformBase+"/api/sdk/api-token", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+sdkToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("API token request failed: %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		APIToken  string `json:"api_token"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parsing API token response: %w", err)
	}

	fmt.Printf("[SDK] Got api_token (expires in %ds)\n", result.ExpiresIn)
	return result.APIToken, nil
}

func FetchE2EERSKey(apiToken string) (string, error) {
	apiBase := "https://api-apse1.plaud.ai"
	payload, _ := json.Marshal(map[string]interface{}{})
	req, _ := http.NewRequest("POST", apiBase+"/e2ee/gen_key", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("E2EE key request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("E2EE key request failed: %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data struct {
			PrivateKeyBase64 string `json:"private_key_base64"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parsing E2EE key response: %w", err)
	}

	keyBytes, err := base64.StdEncoding.DecodeString(result.Data.PrivateKeyBase64)
	if err != nil {
		return "", fmt.Errorf("decoding E2EE key: %w", err)
	}

	return string(keyBytes), nil
}
