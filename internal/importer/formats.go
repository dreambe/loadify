package importer

import (
	"encoding/json"
	"fmt"
	"strings"
)

// --- HAR ---

func parseHAR(data []byte) ([]req, error) {
	var har struct {
		Log struct {
			Entries []struct {
				Request struct {
					Method   string `json:"method"`
					URL      string `json:"url"`
					Headers  []struct{ Name, Value string } `json:"headers"`
					PostData struct {
						Text string `json:"text"`
					} `json:"postData"`
				} `json:"request"`
			} `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(data, &har); err != nil {
		return nil, fmt.Errorf("importer: invalid HAR JSON: %w", err)
	}
	var out []req
	for _, e := range har.Log.Entries {
		r := req{Method: e.Request.Method, URL: e.Request.URL, Body: e.Request.PostData.Text, Headers: map[string]string{}}
		for _, h := range e.Request.Headers {
			if strings.HasPrefix(h.Name, ":") { // skip HTTP/2 pseudo-headers
				continue
			}
			r.Headers[h.Name] = h.Value
		}
		out = append(out, r)
	}
	return out, nil
}

// --- Postman collection (v2.x) ---

func parsePostman(data []byte) ([]req, error) {
	var col struct {
		Item []postmanItem `json:"item"`
	}
	if err := json.Unmarshal(data, &col); err != nil {
		return nil, fmt.Errorf("importer: invalid Postman JSON: %w", err)
	}
	var out []req
	var walk func(items []postmanItem)
	walk = func(items []postmanItem) {
		for _, it := range items {
			if len(it.Item) > 0 {
				walk(it.Item) // folder
				continue
			}
			if it.Request == nil {
				continue
			}
			r := req{Name: it.Name, Method: it.Request.Method, Headers: map[string]string{}}
			r.URL = it.Request.urlString()
			for _, h := range it.Request.Header {
				if h.Disabled {
					continue
				}
				r.Headers[h.Key] = h.Value
			}
			if it.Request.Body != nil {
				r.Body = it.Request.Body.Raw
			}
			out = append(out, r)
		}
	}
	walk(col.Item)
	return out, nil
}

type postmanItem struct {
	Name    string         `json:"name"`
	Item    []postmanItem  `json:"item"`
	Request *postmanRequest `json:"request"`
}

type postmanRequest struct {
	Method string `json:"method"`
	Header []struct {
		Key      string `json:"key"`
		Value    string `json:"value"`
		Disabled bool   `json:"disabled"`
	} `json:"header"`
	Body *struct {
		Raw string `json:"raw"`
	} `json:"body"`
	URL json.RawMessage `json:"url"`
}

// urlString handles Postman's url being either a string or an object {raw:...}.
func (p *postmanRequest) urlString() string {
	if len(p.URL) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(p.URL, &s) == nil {
		return s
	}
	var o struct {
		Raw string `json:"raw"`
	}
	if json.Unmarshal(p.URL, &o) == nil {
		return o.Raw
	}
	return ""
}

// --- OpenAPI / Swagger (JSON) ---

func parseOpenAPI(data []byte) ([]req, error) {
	var doc struct {
		Servers []struct {
			URL string `json:"url"`
		} `json:"servers"`
		Host     string                            `json:"host"`     // swagger 2.0
		BasePath string                            `json:"basePath"` // swagger 2.0
		Schemes  []string                          `json:"schemes"`  // swagger 2.0
		Paths    map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("importer: invalid OpenAPI JSON: %w", err)
	}
	base := ""
	if len(doc.Servers) > 0 {
		base = strings.TrimRight(doc.Servers[0].URL, "/")
	} else if doc.Host != "" {
		scheme := "https"
		if len(doc.Schemes) > 0 {
			scheme = doc.Schemes[0]
		}
		base = scheme + "://" + doc.Host + strings.TrimRight(doc.BasePath, "/")
	}
	if base == "" {
		return nil, fmt.Errorf("importer: OpenAPI document has no server URL (set servers[].url or host); cannot build absolute request URLs")
	}
	methods := map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true, "head": true}
	var out []req
	for path, ops := range doc.Paths {
		for method := range ops {
			if !methods[strings.ToLower(method)] {
				continue
			}
			out = append(out, req{
				Name:   strings.ToUpper(method) + " " + path,
				Method: strings.ToUpper(method),
				URL:    base + path,
			})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("importer: no operations found in OpenAPI document")
	}
	return out, nil
}
