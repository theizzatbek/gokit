package service

import (
	"os"

	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/fibermap/openapi"
	"gopkg.in/yaml.v3"
)

// openapiYAML mirrors the top-level `openapi:` block in routes.yaml.
// Lives here (not in fibermap) to avoid a fibermap → openapi import cycle.
type openapiYAML struct {
	Info               *openapiInfoYAML             `yaml:"info,omitempty"`
	Servers            []openapiServerYAML          `yaml:"servers,omitempty"`
	SecuritySchemes    map[string]openapiSchemeYAML `yaml:"security_schemes,omitempty"`
	MiddlewareSecurity map[string][]string          `yaml:"middleware_security,omitempty"`
}

type openapiInfoYAML struct {
	Title       string              `yaml:"title"`
	Version     string              `yaml:"version"`
	Description string              `yaml:"description,omitempty"`
	Contact     *openapiContactYAML `yaml:"contact,omitempty"`
}

type openapiContactYAML struct {
	Name  string `yaml:"name,omitempty"`
	URL   string `yaml:"url,omitempty"`
	Email string `yaml:"email,omitempty"`
}

type openapiServerYAML struct {
	URL         string `yaml:"url"`
	Description string `yaml:"description,omitempty"`
}

type openapiSchemeYAML struct {
	Type             string `yaml:"type"`
	Description      string `yaml:"description,omitempty"`
	Scheme           string `yaml:"scheme,omitempty"`
	BearerFormat     string `yaml:"bearer_format,omitempty"`
	Name             string `yaml:"name,omitempty"`
	In               string `yaml:"in,omitempty"`
	OpenIDConnectURL string `yaml:"open_id_connect_url,omitempty"`
}

// openapiEnvelope captures only the openapi key from routes.yaml.
type openapiEnvelope struct {
	OpenAPI *openapiYAML `yaml:"openapi"`
}

// parseOpenAPIBlock reads routesPath and extracts the top-level
// `openapi:` block. Returns nil if the block is absent. Returns
// *errs.Error{Code: CodeOpenAPIYAMLParse} on malformed YAML.
func parseOpenAPIBlock(routesPath string) (*openapiYAML, error) {
	b, err := os.ReadFile(routesPath)
	if err != nil {
		return nil, xerrs.Wrapf(err, xerrs.KindNotFound, CodeOpenAPIYAMLParse,
			"service: read routes yaml %q", routesPath)
	}
	var env openapiEnvelope
	if err := yaml.Unmarshal(b, &env); err != nil {
		return nil, xerrs.Wrapf(err, xerrs.KindValidation, CodeOpenAPIYAMLParse,
			"service: parse openapi block from %q", routesPath)
	}
	return env.OpenAPI, nil
}

// toOpenAPIOptions translates parsed YAML into openapi.Option values.
// Order matters: accumulating options (Servers, SecuritySchemes,
// MiddlewareSecurity) append; Info is single-write.
func (y *openapiYAML) toOpenAPIOptions() []openapi.Option {
	var opts []openapi.Option
	if y.Info != nil {
		info := openapi.Info{
			Title:       y.Info.Title,
			Version:     y.Info.Version,
			Description: y.Info.Description,
		}
		if y.Info.Contact != nil {
			info.Contact = &openapi.Contact{
				Name:  y.Info.Contact.Name,
				URL:   y.Info.Contact.URL,
				Email: y.Info.Contact.Email,
			}
		}
		opts = append(opts, openapi.WithInfo(info))
	}
	for _, s := range y.Servers {
		opts = append(opts, openapi.WithServer(s.URL, s.Description))
	}
	for name, scheme := range y.SecuritySchemes {
		opts = append(opts, openapi.WithSecurity(name, openapi.SecurityScheme{
			Type:             scheme.Type,
			Description:      scheme.Description,
			Scheme:           scheme.Scheme,
			BearerFormat:     scheme.BearerFormat,
			Name:             scheme.Name,
			In:               scheme.In,
			OpenIDConnectURL: scheme.OpenIDConnectURL,
		}))
	}
	for mw, schemes := range y.MiddlewareSecurity {
		for _, schemeName := range schemes {
			opts = append(opts, openapi.MapMiddlewareToSecurity(mw, schemeName))
		}
	}
	return opts
}
