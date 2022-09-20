package authorizer

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/confidential"
	"github.com/niontive/wi-acrpull/pkg/authorizer/types"
)

// TODO - read this from a config
const authorityHost = "https://login.microsoftonline.com/"

func AcquireACRAccessToken(ctx context.Context, clientID, tenantID, acrFQDN string) (types.AccessToken, error) {
	// Get auth token from service account token
	cred := confidential.NewCredFromAssertionCallback(func(context.Context, confidential.AssertionRequestOptions) (string, error) {
		return readJWTFromFS()
	})

	confidentialClientApp, err := confidential.New(
		clientID,
		cred,
		confidential.WithAuthority(fmt.Sprintf("%s%s/oauth2/token", authorityHost, tenantID)))
	if err != nil {
		return "", fmt.Errorf("Unable to get new confidential client app: %w", err)
	}

	authResult, err := confidentialClientApp.AcquireTokenByCredential(ctx, []string{"/.default"})
	if err != nil {
		return "", fmt.Errorf("Unable to acquire bearer token: %w", err)
	}

	// Use auth token to exchange for ACR token
	exchangeURL := fmt.Sprintf("https://%s/oauth2/exchange", acrFQDN)
	ul, err := url.Parse(exchangeURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse token exchange url: %w", err)
	}
	parameters := url.Values{}
	parameters.Add("grant_type", "access_token")
	parameters.Add("service", ul.Hostname())
	parameters.Add("tenant", tenantID)
	parameters.Add("access_token", string(authResult.AccessToken))

	req, err := http.NewRequest("POST", exchangeURL, strings.NewReader(parameters.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to construct token exchange reqeust: %w", err)
	}

	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Add("Content-Length", strconv.Itoa(len(parameters.Encode())))

	client := &http.Client{}
	var resp *http.Response
	defer closeResponse(resp)

	resp, err = client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send token exchange request: %w", err)
	}

	if resp.StatusCode != 200 {
		responseBytes, _ := ioutil.ReadAll(resp.Body)
		return "", fmt.Errorf("ACR token exchange endpoint returned error status: %d. body: %s", resp.StatusCode, string(responseBytes))
	}

	responseBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read request body: %w", err)
	}

	var tokenResp tokenResponse
	err = json.Unmarshal(responseBytes, &tokenResp)
	if err != nil {
		return "", fmt.Errorf("failed to read token exchange response: %w. response: %s", err, string(responseBytes))
	}

	return types.AccessToken(tokenResp.RefreshToken), nil
}

func readJWTFromFS() (string, error) {
	const SATokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"

	f, err := os.ReadFile(SATokenPath)
	if err != nil {
		return "", err
	}

	return string(f), nil
}

func closeResponse(resp *http.Response) {
	if resp == nil {
		return
	}
	resp.Body.Close()
}
