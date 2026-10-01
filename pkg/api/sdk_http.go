package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	lighterhttp "github.com/elliottech/lighter-go/client/http"
)

// sdkHTTP is a MinimalHTTPClient that shares the rotating proxy client.
type sdkHTTP struct {
	endpoint string
	http     *http.Client
}

func (s *sdkHTTP) GetNextNonce(accountIndex int64, apiKeyIndex uint8) (int64, error) {
	result := &lighterhttp.NextNonce{}
	err := s.get("api/v1/nextNonce", map[string]any{
		"account_index": accountIndex,
		"api_key_index": apiKeyIndex,
	}, result)
	if err != nil {
		return -1, err
	}
	return result.Nonce, nil
}

func (s *sdkHTTP) GetApiKey(accountIndex int64, apiKeyIndex uint8) (string, error) {
	result := &lighterhttp.AccountApiKeys{}
	err := s.get("api/v1/apikeys", map[string]any{
		"account_index": accountIndex,
		"api_key_index": apiKeyIndex,
	}, result)
	if err != nil {
		return "", err
	}
	if len(result.ApiKeys) == 0 {
		return "", fmt.Errorf("no api keys returned")
	}
	return result.ApiKeys[0].PublicKey, nil
}

func (s *sdkHTTP) get(path string, params map[string]any, result interface{}) error {
	u, err := url.Parse(s.endpoint)
	if err != nil {
		return err
	}
	u.Path = path
	q := u.Query()
	for k, v := range params {
		q.Set(k, fmt.Sprintf("%v", v))
	}
	u.RawQuery = q.Encode()

	resp, err := s.http.Get(u.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	var status lighterhttp.ResultCode
	if err := json.Unmarshal(body, &status); err != nil {
		return err
	}
	if status.Code != lighterhttp.CodeOK {
		return fmt.Errorf("%s", status.Message)
	}
	return json.Unmarshal(body, result)
}
