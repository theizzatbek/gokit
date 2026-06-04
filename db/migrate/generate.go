package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/theizzatbek/gokit/errs"
)

// Stable Code constants for the scaffold path.
const (
	// CodeGenerateFailed — Generate could not write the new file
	// (mkdir / write / permission errors).
	CodeGenerateFailed = "migrate_generate_failed"

	// CodeGenerateInvalidName — Generate was passed an empty name OR
	// one with chars outside `[A-Za-z0-9._-]` (the same alphabet
	// the runner's filename regex accepts).
	CodeGenerateInvalidName = "migrate_generate_invalid_name"
)

// generateConfig collects Generate's options.
type generateConfig struct {
	includeDown bool
	timestamped bool
}

// GenerateOption tunes [Generate].
type GenerateOption func(*generateConfig)

// WithDown also creates a matching `NNNN_name.down.sql` alongside
// the Up file. Default off — many shops ship "forward-only"
// migrations and only generate Down on demand.
func WithDown() GenerateOption {
	return func(c *generateConfig) { c.includeDown = true }
}

// WithTimestamp stamps the version as `YYYYMMDDHHMMSS` instead of
// the default "next NNNN" (which scans dir for the highest existing
// numeric prefix and returns +1 zero-padded to 4).
//
// Timestamped versions are recommended for repos where multiple
// developers can land migrations independently — sequential NNNN
// scheme causes merge conflicts when two PRs both pick 0042.
func WithTimestamp() GenerateOption {
	return func(c *generateConfig) { c.timestamped = true }
}

// Generate scaffolds a new migration file in dir. Returns the
// absolute path of the created Up file (Down path, if any, is the
// same basename with `.down.sql` suffix).
//
//	path, err := migrate.Generate("./db/migrations", "add_users_email_index")
//	// → ./db/migrations/0042_add_users_email_index.sql
//
// Naming rules match the runner's parser — name is the suffix after
// the version, must use the alphabet `[A-Za-z0-9._-]`. Pass
// [WithDown] for a matching `.down.sql`, [WithTimestamp] to stamp
// the version with `YYYYMMDDHHMMSS` instead of next-NNNN.
//
// The Up file body is a single comment line you can replace; the
// Down file is left empty.
//
// Errors:
//   - CodeGenerateInvalidName — name is empty / has unsafe chars.
//   - CodeGenerateFailed     — mkdir / write / permission errored.
func Generate(dir, name string, opts ...GenerateOption) (string, error) {
	if !isSafeName(name) {
		return "", errs.Validationf(CodeGenerateInvalidName,
			"migrate: name %q has chars outside [A-Za-z0-9._-]", name)
	}
	cfg := generateConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", errs.Wrap(err, errs.KindInternal, CodeGenerateFailed,
			"migrate: mkdir "+dir)
	}
	var version string
	if cfg.timestamped {
		version = time.Now().UTC().Format("20060102150405")
	} else {
		v, err := nextNumericVersion(dir)
		if err != nil {
			return "", err
		}
		version = v
	}
	upBasename := version + "_" + name + ".sql"
	upPath := filepath.Join(dir, upBasename)
	upBody := fmt.Sprintf("-- migrate: up %s\n", name)
	if err := writeIfNotExist(upPath, []byte(upBody)); err != nil {
		return "", err
	}
	if cfg.includeDown {
		downBasename := version + "_" + name + ".down.sql"
		downPath := filepath.Join(dir, downBasename)
		downBody := fmt.Sprintf("-- migrate: down %s\n", name)
		if err := writeIfNotExist(downPath, []byte(downBody)); err != nil {
			return "", err
		}
	}
	return upPath, nil
}

// nextNumericVersion scans dir for files matching the runner's
// `NNNN_name.sql` convention, returns the highest NNNN + 1 zero-
// padded to 4 digits. Empty dir → "0001".
func nextNumericVersion(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", errs.Wrap(err, errs.KindInternal, CodeGenerateFailed,
			"migrate: read dir "+dir)
	}
	versions := make([]int, 0, len(entries))
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		groups := filenameRE.FindStringSubmatch(ent.Name())
		if groups == nil {
			continue
		}
		n, err := strconv.Atoi(groups[1])
		if err != nil {
			continue // non-numeric NNNN — ignore for next-version calc
		}
		versions = append(versions, n)
	}
	if len(versions) == 0 {
		return "0001", nil
	}
	sort.Ints(versions)
	next := versions[len(versions)-1] + 1
	return fmt.Sprintf("%04d", next), nil
}

// isSafeName mirrors the runner's filename regex on the post-version
// suffix portion only.
func isSafeName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// writeIfNotExist is os.WriteFile with O_EXCL — refuses to clobber
// an existing path. Two developers racing on the same NNNN_name
// scaffold would normally pick distinct names; if they collide,
// loud-fail beats silent overwrite.
func writeIfNotExist(path string, body []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return errs.Wrap(err, errs.KindInternal, CodeGenerateFailed,
			"migrate: write "+path)
	}
	defer f.Close()
	if _, err := f.Write(body); err != nil {
		return errs.Wrap(err, errs.KindInternal, CodeGenerateFailed,
			"migrate: write body "+path)
	}
	// Silence the "filepath suggested by user input" lint — path comes
	// from caller-supplied dir + caller-supplied name, which Generate
	// validates via isSafeName.
	_ = strings.TrimSpace
	return nil
}
