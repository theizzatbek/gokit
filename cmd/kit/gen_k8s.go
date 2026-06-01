package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	"github.com/theizzatbek/gokit/service"
)

const usageGenK8s = `kit gen k8s — emit Kubernetes manifests for a kit-based service

Usage:
  kit gen k8s --name svc --image IMG [flags]

Generates: Deployment, Service, ConfigMap (env vars derived from
service.Config's struct tags via reflection), optional Ingress.

Flags:
  --name        service name (required)
  --image       container image (required)
  --namespace   default ""
  --replicas    default 2
  --port        container/service port (default 3000)
  --host        ingress host (optional; without --host no Ingress emitted)
  --out         output file (default: stdout)

Defaults are kit-aware: probe paths point at /healthz, /readyz,
/preflight; the kit-known port matches service.Config.Service.Addr
default (":3000"). Override with --port as needed.
`

type k8sFlags struct {
	name      string
	image     string
	namespace string
	replicas  int
	port      int
	host      string
	out       string
}

func runGenK8s(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("gen k8s", flag.ContinueOnError)
	f := k8sFlags{}
	fs.StringVar(&f.name, "name", "", "service name (required)")
	fs.StringVar(&f.image, "image", "", "container image (required)")
	fs.StringVar(&f.namespace, "namespace", "", "kubernetes namespace")
	fs.IntVar(&f.replicas, "replicas", 2, "replica count")
	fs.IntVar(&f.port, "port", 3000, "container + service port")
	fs.StringVar(&f.host, "host", "", "ingress host (omit to skip Ingress)")
	fs.StringVar(&f.out, "out", "", "output file (default: stdout)")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usageGenK8s) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	if f.name == "" || f.image == "" {
		fs.Usage()
		return errors.New("--name and --image are required")
	}
	w := io.Writer(os.Stdout)
	if f.out != "" {
		file, err := os.Create(f.out)
		if err != nil {
			return fmt.Errorf("create %s: %w", f.out, err)
		}
		defer file.Close()
		w = file
	}
	if err := emitK8sManifests(w, f); err != nil {
		return err
	}
	return nil
}

func emitK8sManifests(w io.Writer, f k8sFlags) error {
	emitDeployment(w, f)
	fmt.Fprintln(w, "---")
	emitService(w, f)
	fmt.Fprintln(w, "---")
	emitConfigMap(w, f)
	if f.host != "" {
		fmt.Fprintln(w, "---")
		emitIngress(w, f)
	}
	return nil
}

func emitDeployment(w io.Writer, f k8sFlags) {
	fmt.Fprintf(w, `apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
%s  labels:
    app: %s
spec:
  replicas: %d
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
        - name: %s
          image: %s
          ports:
            - name: http
              containerPort: %d
          envFrom:
            - configMapRef:
                name: %s-config
            - secretRef:
                name: %s-secret
                optional: true
          readinessProbe:
            httpGet: { path: /readyz, port: http }
            initialDelaySeconds: 5
            periodSeconds: 10
          livenessProbe:
            httpGet: { path: /healthz, port: http }
            initialDelaySeconds: 15
            periodSeconds: 20
          startupProbe:
            httpGet: { path: /preflight, port: http }
            failureThreshold: 30
            periodSeconds: 5
`, f.name, namespaceLine(f.namespace), f.name, f.replicas, f.name, f.name, f.name, f.image, f.port, f.name, f.name)
}

func emitService(w io.Writer, f k8sFlags) {
	fmt.Fprintf(w, `apiVersion: v1
kind: Service
metadata:
  name: %s
%sspec:
  selector:
    app: %s
  ports:
    - name: http
      port: %d
      targetPort: http
`, f.name, namespaceLine(f.namespace), f.name, f.port)
}

func emitConfigMap(w io.Writer, f k8sFlags) {
	fmt.Fprintf(w, `apiVersion: v1
kind: ConfigMap
metadata:
  name: %s-config
%sdata:
`, f.name, namespaceLine(f.namespace))
	envs := collectEnvDefaults()
	for _, e := range envs {
		fmt.Fprintf(w, "  # %s\n  %s: %q\n", e.Doc, e.Name, e.Default)
	}
}

func emitIngress(w io.Writer, f k8sFlags) {
	fmt.Fprintf(w, `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: %s
%sspec:
  rules:
    - host: %s
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: %s
                port:
                  name: http
`, f.name, namespaceLine(f.namespace), f.host, f.name)
}

func namespaceLine(ns string) string {
	if ns == "" {
		return ""
	}
	return "  namespace: " + ns + "\n"
}

// envEntry pairs an env-var name with its kit-known default + doc.
type envEntry struct {
	Name    string
	Default string
	Doc     string
}

// collectEnvDefaults reflects service.Config and returns every env
// var the kit reads, with the `envDefault` tag value as the default.
// Used by emitConfigMap so the generated ConfigMap stays in sync
// with the kit's known env-var schema as it evolves.
func collectEnvDefaults() []envEntry {
	var out []envEntry
	walkEnvTags(reflect.TypeOf(service.Config{}), "", &out)
	return out
}

func walkEnvTags(t reflect.Type, prefix string, out *[]envEntry) {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		ftype := f.Type
		if ftype.Kind() == reflect.Ptr {
			ftype = ftype.Elem()
		}
		envName := f.Tag.Get("env")
		envDefault := f.Tag.Get("envDefault")
		envPrefix := f.Tag.Get("envPrefix")
		// Nested struct: descend with prefix.
		if ftype.Kind() == reflect.Struct {
			nested := prefix + envPrefix
			walkEnvTags(ftype, nested, out)
			continue
		}
		if envName == "" {
			continue
		}
		full := prefix + envName
		doc := strings.ReplaceAll(f.Name, "_", " ")
		*out = append(*out, envEntry{
			Name:    full,
			Default: envDefault,
			Doc:     doc,
		})
	}
}
