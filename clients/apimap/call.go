package apimap

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Call carries per-invocation parameters: path-variable values, query
// overrides, additional headers, and (for Do) the request body.
type Call struct {
	Path    map[string]string // {var} substitution; values URL-escaped
	Query   url.Values        // merged into the endpoint's URL
	Headers http.Header       // merged over endpoint + default headers
	Body    any               // used by Do; ignored by Exchange (body arg wins)
}

// substitutePath expands {var} placeholders in template with URL-escaped
// values from path. Missing or unknown variables return *errs.Error.
func substitutePath(template string, vars []string, path map[string]string) (string, error) {
	if len(path) > 0 {
		declared := map[string]struct{}{}
		for _, v := range vars {
			declared[v] = struct{}{}
		}
		for k := range path {
			if _, ok := declared[k]; !ok {
				return "", xerrs.Validationf(CodeUnknownPathVar,
					"apimap: path variable %q not declared by endpoint", k)
			}
		}
	}

	var b strings.Builder
	i := 0
	for i < len(template) {
		if template[i] != '{' {
			b.WriteByte(template[i])
			i++
			continue
		}
		end := indexClose(template, i+1)
		if end < 0 {
			return "", xerrs.Validation(CodeInvalidPathVar,
				"apimap: malformed path template "+template)
		}
		name := template[i+1 : end]
		val, ok := path[name]
		if !ok {
			return "", xerrs.Validationf(CodeMissingPathVar,
				"apimap: path variable %q not provided", name)
		}
		b.WriteString(url.PathEscape(val))
		i = end + 1
	}
	return b.String(), nil
}

// encodeBody serialises body per the encode mode and returns the
// io.Reader to set on req.Body plus the Content-Type header (or "" when
// the caller must supply it).
func encodeBody(mode string, body any) (io.Reader, string, error) {
	switch mode {
	case "", "none":
		return nil, "", nil
	case "json":
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, "", xerrs.Wrap(err, xerrs.KindInternal, CodeEncodeFailed,
				"apimap: json encode failed")
		}
		return bytes.NewReader(buf), "application/json", nil
	case "form":
		var values url.Values
		switch v := body.(type) {
		case url.Values:
			values = v
		case map[string]string:
			values = url.Values{}
			for k, val := range v {
				values.Set(k, val)
			}
		default:
			return nil, "", xerrs.Validationf(CodeUnsupportedBodyType,
				"apimap: form encode requires url.Values or map[string]string, got %T", body)
		}
		return strings.NewReader(values.Encode()), "application/x-www-form-urlencoded", nil
	case "raw":
		r, ok := body.(io.Reader)
		if !ok {
			return nil, "", xerrs.Validationf(CodeUnsupportedBodyType,
				"apimap: raw encode requires io.Reader, got %T", body)
		}
		return r, "", nil
	}
	return nil, "", xerrs.Validationf(CodeInvalidEncode,
		"apimap: unsupported encode mode %q", mode)
}

// mergeHeaders applies precedence: defaults < endpoint < call. Returns a
// fresh http.Header (the inputs are never mutated).
func mergeHeaders(defaults, endpoint map[string]string, call http.Header) http.Header {
	out := http.Header{}
	for k, v := range defaults {
		out.Set(k, v)
	}
	for k, v := range endpoint {
		out.Set(k, v)
	}
	for k, vs := range call {
		out[http.CanonicalHeaderKey(k)] = append([]string(nil), vs...)
	}
	return out
}
