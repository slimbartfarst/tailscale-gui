module github.com/slimbartfarst/tailscale-gui

go 1.23.1

require (
	fyne.io/systray v1.11.0
	golang.org/x/net v0.30.0
	tailscale.com v1.78.0
)

require (
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/fxamacker/cbor/v2 v2.6.0 // indirect
	github.com/go-json-experiment/json v0.0.0-20231102232822-2e55bd4e08b0 // indirect
	github.com/godbus/dbus/v5 v5.1.1-0.20230522191255-76236955d466 // indirect
	github.com/hdevalence/ed25519consensus v0.2.0 // indirect
	github.com/josharian/native v1.1.1-0.20230202152459-5c7d0dd6ab86 // indirect
	github.com/jsimonetti/rtnetlink v1.4.0 // indirect
	github.com/mdlayher/netlink v1.7.2 // indirect
	github.com/mdlayher/socket v0.5.0 // indirect
	github.com/mitchellh/go-ps v1.0.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go4.org/mem v0.0.0-20220726221520-4f986261bf13 // indirect
	go4.org/netipx v0.0.0-20231129151722-fdeea329fbba // indirect
	golang.org/x/crypto v0.28.0 // indirect
	golang.org/x/exp v0.0.0-20240119083558-1b970713d09a // indirect
	golang.org/x/sync v0.9.0 // indirect
	golang.org/x/sys v0.27.0 // indirect
	golang.org/x/text v0.19.0 // indirect
)

// replace directives map vanity import domains to their GitHub sources.
// Required in network-restricted environments; harmless otherwise.
// On GitHub Actions, proxy.golang.org handles these automatically.
replace (
	dario.cat/mergo => github.com/imdario/mergo v1.0.0
	filippo.io/edwards25519 => github.com/FiloSottile/edwards25519 v1.1.0
	fyne.io/systray => github.com/fyne-io/systray v1.11.0
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp => github.com/open-telemetry/opentelemetry-go-contrib/instrumentation/net/http/otelhttp v0.57.0
	go.opentelemetry.io/otel => github.com/open-telemetry/opentelemetry-go v1.32.0
	go.opentelemetry.io/otel/metric => github.com/open-telemetry/opentelemetry-go/metric v1.32.0
	go.opentelemetry.io/otel/trace => github.com/open-telemetry/opentelemetry-go/trace v1.32.0
	go.uber.org/automaxprocs => github.com/uber-go/automaxprocs v1.5.3
	go.uber.org/multierr => github.com/uber-go/multierr v1.11.0
	go.uber.org/zap => github.com/uber-go/zap v1.27.0
	go4.org/mem => github.com/go4org/mem v0.0.0-20220726221520-4f986261bf13
	go4.org/netipx => github.com/go4org/netipx v0.0.0-20231129151722-fdeea329fbba
	golang.org/x/crypto => github.com/golang/crypto v0.28.0
	golang.org/x/exp => github.com/golang/exp v0.0.0-20240119083558-1b970713d09a
	golang.org/x/image => github.com/golang/image v0.18.0
	golang.org/x/mod => github.com/golang/mod v0.19.0
	golang.org/x/net => github.com/golang/net v0.30.0
	golang.org/x/oauth2 => github.com/golang/oauth2 v0.16.0
	golang.org/x/sync => github.com/golang/sync v0.9.0
	golang.org/x/sys => github.com/golang/sys v0.27.0
	golang.org/x/term => github.com/golang/term v0.25.0
	golang.org/x/text => github.com/golang/text v0.19.0
	golang.org/x/time => github.com/golang/time v0.5.0
	golang.org/x/tools => github.com/golang/tools v0.23.0
	golang.org/x/xerrors => github.com/golang/xerrors v0.0.0-20240716161551-93cc26a95ae9
	gomodules.xyz/jsonpatch/v2 => github.com/gomodules/jsonpatch/v2 v2.4.0
	google.golang.org/protobuf => github.com/golang/protobuf v1.33.0
	gopkg.in/inf.v0 => github.com/go-inf/inf v0.9.1
	gopkg.in/ini.v1 => github.com/go-ini/ini v1.67.0
	gopkg.in/square/go-jose.v2 => github.com/square/go-jose/v2 v2.6.0
	gopkg.in/warnings.v0 => github.com/go-warnings/warnings v0.1.2
	gopkg.in/yaml.v2 => github.com/go-yaml/yaml v2.4.0+incompatible
	gopkg.in/yaml.v3 => github.com/go-yaml/yaml/v3 v3.0.1
	gvisor.dev/gvisor => github.com/google/gvisor v0.0.0-20240722211153-64c016c92987
	tailscale.com => github.com/tailscale/tailscale v1.78.0
)
