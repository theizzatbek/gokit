package service

import "testing"

func TestWithAPIMap_FlipsAPIMapEnable(t *testing.T) {
	o := &options{}
	WithAPIMap()(o)
	if !o.apimapEnable {
		t.Fatal("WithAPIMap() did not flip apimapEnable")
	}
}

func TestWithNATSMap_FlipsNATSMapEnable(t *testing.T) {
	o := &options{}
	WithNATSMap()(o)
	if !o.natsmapEnable {
		t.Fatal("WithNATSMap() did not flip natsmapEnable")
	}
}

func TestWithRoutes_FlipsRoutesEnable(t *testing.T) {
	o := &options{}
	WithRoutes()(o)
	if !o.routesEnable {
		t.Fatal("WithRoutes() did not flip routesEnable")
	}
}
