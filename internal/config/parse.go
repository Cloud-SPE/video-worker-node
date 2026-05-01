package config

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type yamlConfig struct {
	ProtocolVersion          int              `yaml:"protocol_version"`
	WorkerEthAddress         string           `yaml:"worker_eth_address,omitempty"`
	AuthToken                string           `yaml:"auth_token,omitempty"`
	PaymentDaemon            rawYAMLNode      `yaml:"payment_daemon"`
	Worker                   yamlWorker       `yaml:"worker"`
	Capabilities             []yamlCapability `yaml:"capabilities"`
	ServiceRegistryPublisher rawYAMLNode      `yaml:"service_registry_publisher,omitempty"`
}

type yamlWorker struct {
	HTTPListen          string `yaml:"http_listen"`
	PaymentDaemonSocket string `yaml:"payment_daemon_socket"`
}

type yamlCapability struct {
	Capability string         `yaml:"capability"`
	WorkUnit   string         `yaml:"work_unit"`
	Extra      yamlObject     `yaml:"extra,omitempty"`
	Offerings  []yamlOffering `yaml:"offerings"`
}

type yamlOffering struct {
	ID                  string     `yaml:"id"`
	PricePerWorkUnitWei string     `yaml:"price_per_work_unit_wei"`
	BackendURL          string     `yaml:"backend_url"`
	Constraints         yamlObject `yaml:"constraints,omitempty"`
}

type rawYAMLNode struct {
	Present bool
	Node    *yaml.Node
}

func (r *rawYAMLNode) UnmarshalYAML(value *yaml.Node) error {
	r.Present = true
	clone := *value
	r.Node = &clone
	return nil
}

type yamlObject struct {
	Present bool
	Value   map[string]any
}

func (o *yamlObject) UnmarshalYAML(value *yaml.Node) error {
	o.Present = true
	if value.Kind != yaml.MappingNode {
		if value.Kind == yaml.ScalarNode && value.Tag == "!!null" {
			return errors.New("must be an object when present (got null)")
		}
		return errors.New("must be an object when present")
	}
	return value.Decode(&o.Value)
}

var (
	lowerEthAddressRE     = regexp.MustCompile(`^0x[0-9a-f]{40}$`)
	videoCapabilityNameRE = regexp.MustCompile(`^video:[a-z0-9]+(\.[a-z0-9]+)+$`)
	knownWorkUnits        = map[string]struct{}{
		"token":                 {},
		"character":             {},
		"audio_second":          {},
		"image_step_megapixel":  {},
		"video_frame_megapixel": {},
	}
)

// SharedWorkerConfig is the worker-facing projection of the shared
// worker.yaml contract.
type SharedWorkerConfig struct {
	ProtocolVersion  int32
	APIVersion       int32
	WorkerEthAddress string
	AuthToken        string
	Worker           SharedWorkerSection
	Capabilities     []RegistryCapability
}

// SharedWorkerSection carries the worker-owned fields from worker.yaml.
type SharedWorkerSection struct {
	HTTPListen          string
	PaymentDaemonSocket string
}

// LoadSharedWorker reads and validates the v3 shared worker.yaml file.
func LoadSharedWorker(path string) (*SharedWorkerConfig, error) {
	parsed, err := parseFile(path)
	if err != nil {
		return nil, err
	}
	if err := validateSharedConfig(parsed); err != nil {
		return nil, fmt.Errorf("config: validate: %w", err)
	}
	return projectSharedConfig(parsed), nil
}

func parseFile(path string) (*yamlConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: open %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return parseReader(f)
}

func parseReader(r io.Reader) (*yamlConfig, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)

	var cfg yamlConfig
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: decode: %w", err)
	}
	var tail yamlConfig
	if err := dec.Decode(&tail); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("config: unexpected second YAML document; only one document per file is supported")
		}
		return nil, fmt.Errorf("config: trailing data after first document: %w", err)
	}
	return &cfg, nil
}

func validateSharedConfig(cfg *yamlConfig) error {
	if cfg == nil {
		return errors.New("config: nil shared worker config")
	}
	if cfg.ServiceRegistryPublisher.Present {
		return errors.New("worker.yaml: 'service_registry_publisher' is not supported in v3.0.1")
	}
	if cfg.ProtocolVersion != int(CurrentProtocolVersion) {
		return fmt.Errorf("worker.yaml: protocol_version=%d is not supported by this worker build (CurrentProtocolVersion=%d)", cfg.ProtocolVersion, CurrentProtocolVersion)
	}
	if !cfg.PaymentDaemon.Present {
		return errors.New("worker.yaml: missing 'payment_daemon' section")
	}
	if cfg.WorkerEthAddress != "" && !lowerEthAddressRE.MatchString(strings.TrimSpace(cfg.WorkerEthAddress)) {
		return fmt.Errorf("worker_eth_address: must be a lowercased 0x-prefixed 40-hex address (got %q)", cfg.WorkerEthAddress)
	}
	if strings.TrimSpace(cfg.Worker.HTTPListen) == "" {
		return errors.New("worker.http_listen: required")
	}
	if strings.TrimSpace(cfg.Worker.PaymentDaemonSocket) == "" {
		return errors.New("worker.payment_daemon_socket: required")
	}
	if len(cfg.Capabilities) == 0 {
		return errors.New("capabilities: at least one capability required")
	}
	seenCaps := make(map[string]struct{}, len(cfg.Capabilities))
	for i, capability := range cfg.Capabilities {
		if err := validateCapability(i, capability); err != nil {
			return err
		}
		if _, exists := seenCaps[capability.Capability]; exists {
			return fmt.Errorf("capabilities[%d].capability: duplicate %q", i, capability.Capability)
		}
		seenCaps[capability.Capability] = struct{}{}
	}
	return nil
}

func validateCapability(i int, capability yamlCapability) error {
	prefix := fmt.Sprintf("capabilities[%d]", i)
	if !videoCapabilityNameRE.MatchString(capability.Capability) {
		return fmt.Errorf("%s.capability: must match ^video:[a-z0-9]+(\\.[a-z0-9]+)+$ (got %q)", prefix, capability.Capability)
	}
	if _, ok := knownWorkUnits[capability.WorkUnit]; !ok {
		return fmt.Errorf("%s.work_unit: must be one of %s (got %q)", prefix, strings.Join(sortedKeys(knownWorkUnits), "|"), capability.WorkUnit)
	}
	if err := validateObject(prefix+".extra", capability.Extra); err != nil {
		return err
	}
	if len(capability.Offerings) == 0 {
		return fmt.Errorf("%s.offerings: at least one offering required", prefix)
	}
	seenOfferings := make(map[string]struct{}, len(capability.Offerings))
	for j, offering := range capability.Offerings {
		if err := validateOffering(prefix, j, offering); err != nil {
			return err
		}
		if _, exists := seenOfferings[offering.ID]; exists {
			return fmt.Errorf("%s.offerings[%d].id: duplicate %q within capability", prefix, j, offering.ID)
		}
		seenOfferings[offering.ID] = struct{}{}
	}
	return nil
}

func validateOffering(capPrefix string, index int, offering yamlOffering) error {
	prefix := fmt.Sprintf("%s.offerings[%d]", capPrefix, index)
	if strings.TrimSpace(offering.ID) == "" {
		return fmt.Errorf("%s.id: required", prefix)
	}
	price, ok := new(big.Int).SetString(offering.PricePerWorkUnitWei, 10)
	if !ok {
		return fmt.Errorf("%s.price_per_work_unit_wei: %q is not a decimal integer", prefix, offering.PricePerWorkUnitWei)
	}
	if price.Sign() <= 0 {
		return fmt.Errorf("%s.price_per_work_unit_wei: must be > 0 (got %q)", prefix, offering.PricePerWorkUnitWei)
	}
	if strings.TrimSpace(offering.BackendURL) == "" {
		return fmt.Errorf("%s.backend_url: required", prefix)
	}
	parsed, err := url.Parse(offering.BackendURL)
	if err != nil {
		return fmt.Errorf("%s.backend_url: %w", prefix, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%s.backend_url: must be an absolute URL", prefix)
	}
	if err := validateObject(prefix+".constraints", offering.Constraints); err != nil {
		return err
	}
	return nil
}

func validateObject(path string, obj yamlObject) error {
	if !obj.Present {
		return nil
	}
	if obj.Value == nil {
		return fmt.Errorf("%s: must be an object when present", path)
	}
	return nil
}

func projectSharedConfig(cfg *yamlConfig) *SharedWorkerConfig {
	out := &SharedWorkerConfig{
		ProtocolVersion:  CurrentProtocolVersion,
		APIVersion:       CurrentAPIVersion,
		WorkerEthAddress: strings.TrimSpace(cfg.WorkerEthAddress),
		AuthToken:        strings.TrimSpace(cfg.AuthToken),
		Worker: SharedWorkerSection{
			HTTPListen:          strings.TrimSpace(cfg.Worker.HTTPListen),
			PaymentDaemonSocket: strings.TrimSpace(cfg.Worker.PaymentDaemonSocket),
		},
		Capabilities: make([]RegistryCapability, 0, len(cfg.Capabilities)),
	}
	for _, capability := range cfg.Capabilities {
		projected := RegistryCapability{
			Name:      capability.Capability,
			WorkUnit:  capability.WorkUnit,
			Extra:     cloneMap(capability.Extra.Value),
			Offerings: make([]RegistryOffering, 0, len(capability.Offerings)),
		}
		for _, offering := range capability.Offerings {
			projected.Offerings = append(projected.Offerings, RegistryOffering{
				ID:                  offering.ID,
				PricePerWorkUnitWei: offering.PricePerWorkUnitWei,
				BackendURL:          offering.BackendURL,
				Constraints:         cloneMap(offering.Constraints.Value),
			})
		}
		out.Capabilities = append(out.Capabilities, projected)
	}
	return out
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func sortedKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// CheckBearerToken compares a request Authorization header with the
// configured bearer token in constant time.
func CheckBearerToken(header string, token string) bool {
	want := "Bearer " + token
	if len(header) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header), []byte(want)) == 1
}
