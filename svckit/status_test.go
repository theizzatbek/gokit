package svckit

import "testing"

type detailMod struct{}

func (detailMod) Name() string { return "withdetail" }
func (detailMod) Status() any  { return map[string]int{"subscribers": 4} }

func TestStatus_IncludesModsInConnectOrder(t *testing.T) {
	s := &Service[struct{}, struct{}]{
		opts: &options{},
		mods: []Mod{nameOnlyMod{name: "plain"}, detailMod{}},
	}
	s.collectModStatus()

	st := s.Status()

	if len(st.Mods) != 2 {
		t.Fatalf("Mods: want 2, got %d", len(st.Mods))
	}
	if st.Mods[0].Name != "plain" || st.Mods[1].Name != "withdetail" {
		t.Fatalf("connect order not preserved: %+v", st.Mods)
	}
	if st.Mods[0].Detail != nil {
		t.Errorf("mod without Statuser: Detail must be nil, got %#v", st.Mods[0].Detail)
	}
	d, ok := st.Mods[1].Detail.(map[string]int)
	if !ok || d["subscribers"] != 4 {
		t.Errorf("Detail: want map[subscribers:4], got %#v", st.Mods[1].Detail)
	}
}

func TestStatus_NilReceiver(t *testing.T) {
	var s *Service[struct{}, struct{}]
	if got := s.Status(); len(got.Mods) != 0 || got.DB {
		t.Errorf("nil receiver must yield a zero Status, got %+v", got)
	}
}
