package email

import (
	"bytes"
	"fmt"
	htmltpl "html/template"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	texttpl "text/template"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Templates is a thread-safe template registry. Stores parsed
// html/template + text/template pairs keyed by base name. Use
// [Templates.Render] to fill a Message's HTMLBody / TextBody from
// data.
//
// Convention: a template named "welcome" loads from
// "welcome.html.tmpl" + "welcome.txt.tmpl" (either OR both). Missing
// halves render to empty body; Send still validates that at least
// one body part is non-empty.
type Templates struct {
	mu   sync.RWMutex
	html map[string]*htmltpl.Template
	text map[string]*texttpl.Template
}

// NewTemplates returns an empty registry. Populate via [LoadGlob] or
// [LoadFS].
func NewTemplates() *Templates {
	return &Templates{
		html: map[string]*htmltpl.Template{},
		text: map[string]*texttpl.Template{},
	}
}

// LoadGlob loads every `*.html.tmpl` and `*.txt.tmpl` matched by pat
// (forwarded to filepath.Glob). Base name is the file stem with the
// `.html` / `.txt` suffix stripped, e.g. `welcome.html.tmpl` →
// `welcome`. Subsequent loads for the same name overwrite the prior
// parse (test-friendly).
func (t *Templates) LoadGlob(pat string) error {
	matches, err := filepath.Glob(pat)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindValidation, CodeInvalidConfig,
			"email/templates: invalid glob")
	}
	for _, m := range matches {
		if err := t.loadOne(m); err != nil {
			return err
		}
	}
	return nil
}

// LoadFS loads every matching template from an fs.FS (embed.FS-friendly).
func (t *Templates) LoadFS(fsys fs.FS, root string) error {
	return fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		raw, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		return t.parseInto(path, string(raw))
	})
}

func (t *Templates) loadOne(path string) error {
	raw, err := readFile(path)
	if err != nil {
		return err
	}
	return t.parseInto(path, raw)
}

func (t *Templates) parseInto(path, raw string) error {
	base := filepath.Base(path)
	switch {
	case strings.HasSuffix(base, ".html.tmpl"):
		name := strings.TrimSuffix(base, ".html.tmpl")
		tpl, err := htmltpl.New(name).Parse(raw)
		if err != nil {
			return xerrs.Wrap(err, xerrs.KindValidation, CodeTemplateExecFailed,
				"email/templates: html parse failed for "+name)
		}
		t.mu.Lock()
		t.html[name] = tpl
		t.mu.Unlock()
	case strings.HasSuffix(base, ".txt.tmpl"):
		name := strings.TrimSuffix(base, ".txt.tmpl")
		tpl, err := texttpl.New(name).Parse(raw)
		if err != nil {
			return xerrs.Wrap(err, xerrs.KindValidation, CodeTemplateExecFailed,
				"email/templates: text parse failed for "+name)
		}
		t.mu.Lock()
		t.text[name] = tpl
		t.mu.Unlock()
	default:
		// Silently skip files outside the convention so callers
		// can ship a single templates/ directory with mixed
		// content (markdown source, assets, …).
		return nil
	}
	return nil
}

// Render fills msg.HTMLBody and msg.TextBody from the templates
// registered under name. Either side may be empty if the matching
// `*.html.tmpl` / `*.txt.tmpl` wasn't loaded — Send's Validate
// still requires at least one populated body, so callers should
// ship at least one half per template.
//
// Returns *errs.Error{Code: [CodeTemplateNotFound]} when neither
// the html- nor text-half is registered.
func (t *Templates) Render(name string, data any, msg *Message) error {
	t.mu.RLock()
	htpl := t.html[name]
	ttpl := t.text[name]
	t.mu.RUnlock()

	if htpl == nil && ttpl == nil {
		return xerrs.Validationf(CodeTemplateNotFound,
			"email/templates: %q not registered", name)
	}
	if htpl != nil {
		var buf bytes.Buffer
		if err := htpl.Execute(&buf, data); err != nil {
			return xerrs.Wrap(err, xerrs.KindValidation, CodeTemplateExecFailed,
				fmt.Sprintf("email/templates: %q html exec", name))
		}
		msg.HTMLBody = buf.String()
	}
	if ttpl != nil {
		var buf bytes.Buffer
		if err := ttpl.Execute(&buf, data); err != nil {
			return xerrs.Wrap(err, xerrs.KindValidation, CodeTemplateExecFailed,
				fmt.Sprintf("email/templates: %q text exec", name))
		}
		msg.TextBody = buf.String()
	}
	return nil
}
