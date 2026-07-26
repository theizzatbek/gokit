package svckit

import (
	"errors"
	"testing"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// nameOnlyMod is the minimal mod: identity only.
type nameOnlyMod struct{ name string }

func (m nameOnlyMod) Name() string { return m.name }

func TestValidateMods_RejectsDuplicateName(t *testing.T) {
	err := validateMods([]Mod{nameOnlyMod{name: "s3"}, nameOnlyMod{name: "s3"}})

	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeModDuplicate {
		t.Fatalf("want Code=%q, got %#v", CodeModDuplicate, err)
	}
}

func TestValidateMods_RejectsEmptyName(t *testing.T) {
	err := validateMods([]Mod{nameOnlyMod{name: ""}})

	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeModDuplicate {
		t.Fatalf("want Code=%q, got %#v", CodeModDuplicate, err)
	}
}

func TestValidateMods_AcceptsDistinct(t *testing.T) {
	err := validateMods([]Mod{nameOnlyMod{name: "s3"}, nameOnlyMod{name: "s3-backup"}})

	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}
