package generator_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	istionetv1 "istio.io/client-go/pkg/apis/networking/v1"
	istiosecv1 "istio.io/client-go/pkg/apis/security/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
	"bigbang.dev/operator/pkg/generator"
)

func TestIngressGatewayResolution(t *testing.T) {
	shared, err := generator.ParseIngressConfig(`defaults:
  inbound:
    gateways: [edge/public, internal/public]
`, `edge/public:
  podSelector:
    matchLabels: {app: custom-gateway}
    matchExpressions:
      - {key: revision, operator: In, values: [stable]}
  serviceAccountName: custom-sa
`)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, defaults, gateways string
		want                     []string
		wantErr                  string
	}{
		{name: "global defaults and multiple gateways", want: []string{"edge/public", "internal/public"}},
		{name: "package overrides global", defaults: "defaults: {inbound: {gateways: [private/gateway]}}", want: []string{"private/gateway"}},
		{name: "route overrides package", defaults: "defaults: {inbound: {gateways: [private/gateway]}}", gateways: "gateways: [edge/public]", want: []string{"edge/public"}},
		{name: "empty route list does not inherit", gateways: "gateways: []", wantErr: "must be non-empty"},
		{name: "empty package list does not inherit", defaults: "defaults: {inbound: {gateways: []}}", wantErr: "must be non-empty"},
		{name: "empty defaults object inherits", defaults: "defaults: {inbound: {}}", want: []string{"edge/public", "internal/public"}},
		{name: "missing namespace", gateways: "gateways: [public]", wantErr: "must be <namespace>/<name>"},
		{name: "extra slash", gateways: "gateways: [edge/public/extra]", wantErr: "must be <namespace>/<name>"},
		{name: "invalid namespace", gateways: "gateways: [UPPER/public]", wantErr: "must be <namespace>/<name>"},
		{name: "invalid name", gateways: "gateways: ['edge/bad name']", wantErr: "must be <namespace>/<name>"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var pkg bbv1alpha1.Package
			mustYAML(t, fmt.Sprintf(`metadata: {name: app, namespace: app}
spec:
  istio: {enabled: true, ambient: {enabled: true}}
  networkPolicies: {enabled: true}
  routes:
    %s
    inbound:
      web:
        enabled: true
        service: web
        port: 80
        containerPort: 8080
        hosts: [web.example.com]
        %s
`, tt.defaults, tt.gateways), &pkg)
			before := pkg.DeepCopy()
			objects, err := generator.Generate(generator.Input{Package: &pkg, Scheme: buildScheme(t), SharedIngressConfig: shared})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got %v, want %s", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, &pkg) {
				t.Fatal("generation mutated Package input")
			}
			assertIngressResources(t, objects, tt.want, shared)
		})
	}
}

func TestIngressConfigValidation(t *testing.T) {
	for _, tt := range []struct{ name, routes, gateways string }{
		{"unknown route field", "defaults: {outbound: {egressGateway: ns/gw}}", ""},
		{"bad routes YAML", "[", ""},
		{"bad gateway YAML", "", "["},
		{"bad reference", "", "bad: {podSelector: {}, serviceAccountName: gateway}"},
		{"missing selector", "", "ns/gw: {serviceAccountName: gateway}"},
		{"missing identity", "", "ns/gw: {podSelector: {}}"},
		{"invalid identity", "", "ns/gw: {podSelector: {}, serviceAccountName: 'bad/name'}"},
		{"invalid selector", "", "ns/gw: {podSelector: {matchExpressions: [{key: app, operator: Bad}]}, serviceAccountName: gateway}"},
		{"unknown gateway field", "", "ns/gw: {podSelector: {}, serviceAccountName: gateway, typo: true}"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := generator.ParseIngressConfig(tt.routes, tt.gateways); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestIngressMissingDefaultsAndUnreadableConfig(t *testing.T) {
	var pkg bbv1alpha1.Package
	mustYAML(t, `spec:
  routes:
    inbound:
      web: {enabled: true, service: web, port: 80}
`, &pkg)
	in := generator.Input{Package: &pkg, Scheme: buildScheme(t)}
	if _, err := generator.Generate(in); err == nil || !strings.Contains(err.Error(), "gateways[] must be non-empty") {
		t.Fatalf("missing defaults: %v", err)
	}
	in.SharedIngressConfigError = fmt.Errorf("malformed gateways")
	if _, err := generator.Generate(in); err == nil || !strings.Contains(err.Error(), "global config unreadable") {
		t.Fatalf("unreadable config: %v", err)
	}
	mustYAML(t, `spec:
  routes:
    inbound:
      web: {enabled: false}
    outbound:
      external: {enabled: true, hosts: [example.com]}
`, &pkg)
	if _, err := generator.Generate(in); err != nil {
		t.Fatalf("disabled inbound and outbound-only routes should not read shared ingress config: %v", err)
	}
}

func mustYAML(t *testing.T, data string, out any) {
	t.Helper()
	if err := yaml.UnmarshalStrict([]byte(data), out); err != nil {
		t.Fatal(err)
	}
}

func assertIngressResources(t *testing.T, objects []client.Object, want []string, shared *generator.IngressConfig) {
	t.Helper()
	principals := map[string]bool{}
	peers := map[string]bool{}
	var vsCount int
	seen := map[string]bool{}
	for _, obj := range objects {
		key := obj.GetObjectKind().GroupVersionKind().Kind + "/" + obj.GetName()
		if seen[key] {
			t.Fatalf("duplicate resource %s", key)
		}
		seen[key] = true
		switch o := obj.(type) {
		case *istionetv1.VirtualService:
			vsCount++
			if !reflect.DeepEqual(o.Spec.Gateways, want) {
				t.Fatalf("gateways = %v, want %v", o.Spec.Gateways, want)
			}
		case *networkingv1.NetworkPolicy:
			if !strings.HasPrefix(o.Name, "allow-ingress-to-web-") {
				continue
			}
			peer := o.Spec.Ingress[0].From[0]
			ns := peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"]
			peers[ns] = true
			if ns == "edge" && !reflect.DeepEqual(peer.PodSelector, shared.Gateways["edge/public"].PodSelector) {
				t.Fatal("custom selector not used")
			}
			if ns != "edge" && len(peer.PodSelector.MatchLabels["app.kubernetes.io/name"]) == 0 {
				t.Fatal("legacy selector missing")
			}
			if len(o.Spec.Ingress[0].Ports) != 2 || o.Spec.Ingress[0].Ports[0].Port.IntVal != 8080 || o.Spec.Ingress[0].Ports[1].Port.IntVal != 15008 {
				t.Fatal("expected workload port and HBONE")
			}
		case *istiosecv1.AuthorizationPolicy:
			if !strings.HasPrefix(o.Name, "web-") {
				continue
			}
			if o.Spec.Selector.MatchLabels["app.kubernetes.io/name"] != "web" {
				t.Fatal("authorization selector did not inherit route selector")
			}
			principals[o.Spec.Rules[0].From[0].Source.Principals[0]] = true
			if !reflect.DeepEqual(o.Spec.Rules[0].To[0].Operation.Ports, []string{"8080"}) {
				t.Fatal("wrong authorization port")
			}
		}
	}
	if vsCount != 1 || len(peers) != len(want) || len(principals) != len(want) {
		t.Fatalf("inconsistent route resources: vs=%d peers=%v principals=%v", vsCount, peers, principals)
	}
	for _, ref := range want {
		parts := strings.Split(ref, "/")
		sa := parts[1] + "-ingressgateway-service-account"
		if ref == "edge/public" {
			sa = "custom-sa"
		}
		if !peers[parts[0]] || !principals["cluster.local/ns/"+parts[0]+"/sa/"+sa] {
			t.Fatalf("gateway %s missing from policies", ref)
		}
	}
}
