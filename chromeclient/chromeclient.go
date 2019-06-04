package chromeclient

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
    "log"

	"github.com/fasthttp/websocket"
)

type Message struct {
	ID     int         `json:"id"`
	Method string      `json:"method"`
	Params interface{} `json:"params"`
}

type FetchEnableParams struct {
	Patterns []RequestPattern `json:"patterns"`
}

type FetchFulfillRequestParams struct {
	RequestID       string              `json:"requestId"`
	ResponseCode    int                 `json:"responseCode"`
	ResponseHeaders []map[string]string `json:"responseHeaders"`
	Body            string              `json:"body"`
}

func NewFetchFulfillRequestParams(requestID string, code int, headers []map[string]string, body []byte) FetchFulfillRequestParams {
	return FetchFulfillRequestParams{
		RequestID:       requestID,
		ResponseCode:    code,
		ResponseHeaders: headers,
		Body:            base64.StdEncoding.EncodeToString(body),
	}
}

type RequestPattern struct {
	ResourceType string `json:"resourceType"`
}

type ChromeRequest struct {
	URL             string            `json:"url"`
	Method          string            `json:"method"`
	Headers         map[string]string `json:"headers"`
	PostData        string            `json:"postData"`
	HasPostData     bool              `json:"hasPostData"`
	InitialPriority string            `json:"initialPriority"`
	ReferrerPolicy  string            `json:"referrerPolicy"`
}

func (c *ChromeRequest) ToHTTPRequest() (*http.Request, error) {
	url, err := url.Parse(c.URL)
	if err != nil {
		return &http.Request{}, err
	}

	c.Headers["Accept-Encoding"] = "gzip"
	c.Headers["Accept"] = "*/*"

	req, err := http.NewRequest(c.Method, url.String(),
		bytes.NewBuffer([]byte(c.PostData)))
	if err != nil {
		return &http.Request{}, err
	}

	for key, value := range c.Headers {
		req.Header.Add(key, value)
	}
	return req, nil
}

type RequestPausedResponse struct {
	Method string `json:"method"`
	Params struct {
		RequestID    string        `json:"requestId"`
		Request      ChromeRequest `json:"request"`
		FrameID      string        `json:"frameId"`
		ResourceType string        `json:"resourceType"`
	} `json:"params"`
}

type ChromeClient struct {
	Ws       *websocket.Conn
	ID       int
	Version  map[string]string
	patterns []string
}

func (c *ChromeClient) FetchEnable() error {
	var patterns = []RequestPattern{}

	for _, pattern := range c.patterns {
		requestPattern := RequestPattern{
			ResourceType: pattern,
		}
		patterns = append(patterns, requestPattern)
	}

	msg := Message{
		ID:     c.ID,
		Method: "Fetch.enable",
		Params: FetchEnableParams{
			Patterns: patterns,
		},
	}

	err := c.Ws.WriteJSON(msg)
	if err != nil {
		return err
	}

    _, p, err := c.Ws.ReadMessage()
	if err != nil {
		return err
	}
    log.Println(string(p))
	return nil
}

func (c *ChromeClient) Send(params interface{}) error {
	c.ID++
	msg := Message{
		ID:     c.ID,
		Method: "Fetch.fulfillRequest",
		Params: params,
	}

	if err := c.Ws.WriteJSON(msg); err != nil {
		return err
	}

	return nil
}

func (c *ChromeClient) FulfillRequest(requestID string, code int, headers []map[string]string, body []byte) error {
	c.ID++
	msg := Message{
		ID:     c.ID,
		Method: "Fetch.fulfillRequest",
		Params: FetchFulfillRequestParams{
			RequestID:       requestID,
			ResponseCode:    code,
			ResponseHeaders: headers,
			Body:            base64.StdEncoding.EncodeToString(body),
		},
	}
	err := c.Ws.WriteJSON(msg)
	if err != nil {
		return err
	}
	return nil
}

func (c *ChromeClient) Close() {
	c.Ws.Close()
}

func NewChromeClient(host *string, port *string, patterns []string) (ChromeClient, error) {
	var client http.Client
	remoteDebuggingURL := fmt.Sprintf("http://%s:%s", *host, *port)
	resp, err := client.Get(remoteDebuggingURL + "/json/version")
	if err != nil {
		return ChromeClient{}, err
	}
	defer resp.Body.Close()

	bodyBytes, _ := ioutil.ReadAll(resp.Body)

	var version map[string]string
	json.Unmarshal(bodyBytes, &version)
	wsURL := version["webSocketDebuggerUrl"]

	dialer := websocket.Dialer{
		WriteBufferSize: 33554432, // this is fucking bug in gorilla/websocket,
		// if length of sending data less that
		// writeBufferSize then connection is lost
	}

	ws, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		return ChromeClient{}, err
	}
	chromeClient := ChromeClient{
		Ws:       ws,
		ID:       1000,
		Version:  version,
		patterns: patterns,
	}
	return chromeClient, nil

}
