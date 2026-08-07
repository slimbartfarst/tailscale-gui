module github.com/slimbartfarst/tailscale-gui

go 1.26

require (
	fyne.io/systray v1.11.1-0.20250812065214-4856ac3adc3c
	golang.org/x/net v0.53.0
	tailscale.com v1.98.8
)

// replace directives map vanity import domains to their GitHub sources.
// Required in network-restricted environments; harmless when proxy.golang.org
// is available (GitHub Actions uses the proxy which handles these natively).
replace (
	tailscale.com      => github.com/tailscale/tailscale v1.98.8
	fyne.io/systray    => github.com/fyne-io/systray v1.11.1-0.20250812065214-4856ac3adc3c
	golang.org/x/net   => github.com/golang/net v0.53.0
	golang.org/x/crypto => github.com/golang/crypto v0.31.0
	golang.org/x/exp   => github.com/golang/exp v0.0.0-20240119083558-1b970713d09a
	golang.org/x/image => github.com/golang/image v0.18.0
	golang.org/x/mod   => github.com/golang/mod v0.19.0
	golang.org/x/oauth2 => github.com/golang/oauth2 v0.16.0
	golang.org/x/sync  => github.com/golang/sync v0.9.0
	golang.org/x/sys   => github.com/golang/sys v0.30.0
	golang.org/x/term  => github.com/golang/term v0.25.0
	golang.org/x/text  => github.com/golang/text v0.21.0
	golang.org/x/time  => github.com/golang/time v0.5.0
	golang.org/x/tools => github.com/golang/tools v0.23.0
	golang.org/x/xerrors => github.com/golang/xerrors v0.0.0-20240716161551-93cc26a95ae9
	go4.org/mem        => github.com/go4org/mem v0.0.0-20220726221520-4f986261bf13
	go4.org/netipx     => github.com/go4org/netipx v0.0.0-20231129151722-fdeea329fbba
	filippo.io/edwards25519 => github.com/FiloSottile/edwards25519 v1.1.0
	gvisor.dev/gvisor  => github.com/google/gvisor v0.0.0-20240722211153-64c016c92987
	google.golang.org/protobuf => github.com/golang/protobuf v1.36.5
	google.golang.org/appengine => github.com/golang/appengine v1.6.8
	gopkg.in/yaml.v2   => github.com/go-yaml/yaml v2.4.0+incompatible
	gopkg.in/yaml.v3   => github.com/go-yaml/yaml/v3 v3.0.1
	gopkg.in/ini.v1    => github.com/go-ini/ini v1.67.0
	gopkg.in/inf.v0    => github.com/go-inf/inf v0.9.1
	gopkg.in/warnings.v0 => github.com/go-warnings/warnings v0.1.2
	gopkg.in/square/go-jose.v2 => github.com/square/go-jose/v2 v2.6.0
	go.uber.org/zap    => github.com/uber-go/zap v1.27.0
	go.uber.org/multierr => github.com/uber-go/multierr v1.11.0
	go.uber.org/automaxprocs => github.com/uber-go/automaxprocs v1.5.3
	go.opentelemetry.io/otel => github.com/open-telemetry/opentelemetry-go v1.32.0
	go.opentelemetry.io/otel/metric => github.com/open-telemetry/opentelemetry-go/metric v1.32.0
	go.opentelemetry.io/otel/trace => github.com/open-telemetry/opentelemetry-go/trace v1.32.0
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp => github.com/open-telemetry/opentelemetry-go-contrib/instrumentation/net/http/otelhttp v0.57.0
	dario.cat/mergo    => github.com/imdario/mergo v1.0.0
	gomodules.xyz/jsonpatch/v2 => github.com/gomodules/jsonpatch/v2 v2.4.0
)
