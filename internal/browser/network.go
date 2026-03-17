package browser

import (
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/go-rod/rod/lib/proto"
)

// NetworkCapture collects CDP Network events to reconstruct the redirect chain
// and track external hosts and WebSocket connections.
type NetworkCapture struct {
	mu            sync.Mutex
	targetHost    string
	externalHosts map[string]struct{}
	webSockets    map[string]struct{}

	mainRequestID proto.NetworkRequestID
	initialized   bool
	redirectHops  []capturedResponse // hops from RedirectResponse
	finalResponse *capturedResponse  // final response from responseReceived
}

type capturedResponse struct {
	url        string
	statusCode int
	headers    http.Header
	requestID  proto.NetworkRequestID
}

// NewNetworkCapture creates a new capture for the given target host and original URL.
func NewNetworkCapture(targetHost, originalURL string) *NetworkCapture {
	return &NetworkCapture{
		targetHost:    targetHost,
		externalHosts: make(map[string]struct{}),
		webSockets:    make(map[string]struct{}),
	}
}

// HandleRequestWillBeSent processes a Network.requestWillBeSent event.
func (nc *NetworkCapture) HandleRequestWillBeSent(e *proto.NetworkRequestWillBeSent) {
	nc.mu.Lock()
	defer nc.mu.Unlock()

	// Track external hosts from all requests
	if parsed, err := url.Parse(e.Request.URL); err == nil {
		host := parsed.Hostname()
		if host != "" && host != nc.targetHost {
			nc.externalHosts[host] = struct{}{}
		}
	}

	// First request becomes the main request
	if !nc.initialized {
		nc.mainRequestID = e.RequestID
		nc.initialized = true
	}

	// If this event carries a RedirectResponse, capture the redirect hop.
	if e.RedirectResponse != nil {
		nc.redirectHops = append(nc.redirectHops, capturedResponse{
			url:        e.RedirectResponse.URL,
			statusCode: e.RedirectResponse.Status,
			headers:    cdpHeadersToHTTPHeader(e.RedirectResponse.Headers),
			requestID:  e.RequestID,
		})
	}
}

// HandleResponseReceived processes a Network.responseReceived event.
func (nc *NetworkCapture) HandleResponseReceived(e *proto.NetworkResponseReceived) {
	nc.mu.Lock()
	defer nc.mu.Unlock()

	// Only capture the main document response
	if e.RequestID != nc.mainRequestID {
		return
	}

	resp := capturedResponse{
		url:        e.Response.URL,
		statusCode: e.Response.Status,
		headers:    cdpHeadersToHTTPHeader(e.Response.Headers),
		requestID:  e.RequestID,
	}
	nc.finalResponse = &resp
}

// Chain returns the captured redirect chain in order.
func (nc *NetworkCapture) Chain() []capturedResponse {
	nc.mu.Lock()
	defer nc.mu.Unlock()

	chain := make([]capturedResponse, 0, len(nc.redirectHops)+1)
	chain = append(chain, nc.redirectHops...)
	if nc.finalResponse != nil {
		chain = append(chain, *nc.finalResponse)
	}
	return chain
}

// HandleWebSocketCreated processes a Network.webSocketCreated event.
func (nc *NetworkCapture) HandleWebSocketCreated(e *proto.NetworkWebSocketCreated) {
	nc.mu.Lock()
	defer nc.mu.Unlock()

	if e.URL != "" {
		nc.webSockets[e.URL] = struct{}{}
	}
}

// WebSockets returns sorted unique WebSocket URLs.
func (nc *NetworkCapture) WebSockets() []string {
	nc.mu.Lock()
	defer nc.mu.Unlock()
	urls := make([]string, 0, len(nc.webSockets))
	for u := range nc.webSockets {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	return urls
}

// ExternalHosts returns sorted unique external hostnames.
func (nc *NetworkCapture) ExternalHosts() []string {
	nc.mu.Lock()
	defer nc.mu.Unlock()
	hosts := make([]string, 0, len(nc.externalHosts))
	for h := range nc.externalHosts {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	return hosts
}

// cdpHeadersToHTTPHeader converts CDP header format to http.Header.
func cdpHeadersToHTTPHeader(headers proto.NetworkHeaders) http.Header {
	h := make(http.Header)
	for key, val := range headers {
		s := val.String()
		for _, line := range strings.Split(s, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				h.Add(key, line)
			}
		}
	}
	return h
}
