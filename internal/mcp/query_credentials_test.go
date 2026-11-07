package mcp

import (
	"net/url"
	"testing"
)

func TestCredentialQueryStems(t *testing.T) {
	refused := []string{
		"api_key", "apikey", "key", "token", "access_token", "refresh_token", "id_token", "auth",
		"authorization", "secret", "client_secret", "password", "passwd", "pwd", "sig", "signature",
		"X-Amz-Signature", "X-Amz-Credential", "X-Amz-Security-Token", "X-Goog-Signature",
		"session", "sessionid", "jwt", "bearer", "private_token", "apiKey", "accessToken",
		"clientSecret", "AWSAccessKeyId", "Signature", "API_KEY", "ACCESS_TOKEN", "x-api-key",
		"api-key", "Api-Key", "X-Goog-Credential", "Ocp-Apim-Subscription-Key", "subscription-key",
		"subscription_key", "access_key", "secret_key", "oauth_token", "oauth_signature", "xsrf_token",
		"authenticity_token", "sessionToken", "auth_token", "apiToken", "bearer_token", "x-access-token",
		"accesstoken", "refreshtoken", "idtoken", "authtoken", "apitoken", "privatetoken",
		"sessiontoken", "oauthtoken", "csrftoken", "xsrftoken", "clientsecret", "appsecret",
		"secretkey", "privatekey", "accesskey", "appkey", "authkey", "licensekey", "hapikey",
		"userkey", "devkey", "ACCESSTOKEN", "CLIENTSECRET", "tokens", "access_tokens", "api_keys",
		"apiKeys", "keys", "secrets", "passwords", "credentials", "token2", "key1", "apikey2", "sig2",
		"passphrase", "pass", "jsessionid", "auth_code", "access-token", "Authorization", "cookie", "hollis_x",
		"prefix_token_suffix", "mysecretvalue", "authorship", "design",
	}
	for _, name := range refused {
		if err := ValidateURL("url", "https://example.test/mcp?"+url.QueryEscape(name)+"=synthetic"); err == nil {
			t.Errorf("credential stem accepted: %s", name)
		}
	}
	for _, name := range []string{"author", "authority", "keyword", "keywords", "monkey", "tokens_count", "tokensCount",
		"q", "page", "sv", "code", "Expires", "client_assertion", "code_verifier", "PHPSESSID", "sid", "ticket", "pw"} {
		if err := ValidateURL("url", "https://example.test/mcp?"+url.QueryEscape(name)+"=synthetic"); err != nil {
			t.Errorf("non-credential name refused: %s: %v", name, err)
		}
	}
}
