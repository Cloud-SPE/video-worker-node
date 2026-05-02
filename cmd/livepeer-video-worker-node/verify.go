package main

import (
	"fmt"
	"sort"

	"github.com/Cloud-SPE/video-worker-node/internal/config"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/paymentclient"
)

func verifyPaymentDaemonCatalog(cfg config.Config, daemon paymentclient.ListCapabilitiesResult) error {
	want := normalizeConfigCapabilities(cfg.Capabilities)
	got := normalizeDaemonCapabilities(daemon.Capabilities)

	if len(got) != len(want) {
		return fmt.Errorf("payment-daemon catalog mismatch: worker has %d capabilities, daemon has %d", len(want), len(got))
	}
	for i := range want {
		if want[i].Capability != got[i].Capability {
			return fmt.Errorf("payment-daemon catalog mismatch: capability[%d] worker=%q daemon=%q", i, want[i].Capability, got[i].Capability)
		}
		if want[i].WorkUnit != got[i].WorkUnit {
			return fmt.Errorf("payment-daemon catalog mismatch: capability[%d] (%q) work_unit worker=%q daemon=%q", i, want[i].Capability, want[i].WorkUnit, got[i].WorkUnit)
		}
		if len(want[i].Offerings) != len(got[i].Offerings) {
			return fmt.Errorf("payment-daemon catalog mismatch: capability[%d] (%q) offering count worker=%d daemon=%d", i, want[i].Capability, len(want[i].Offerings), len(got[i].Offerings))
		}
		for j := range want[i].Offerings {
			if want[i].Offerings[j].ID != got[i].Offerings[j].ID {
				return fmt.Errorf("payment-daemon catalog mismatch: capability[%d] (%q) offering[%d] id worker=%q daemon=%q", i, want[i].Capability, j, want[i].Offerings[j].ID, got[i].Offerings[j].ID)
			}
		}
	}
	return nil
}

type normalizedCapability struct {
	Capability string
	WorkUnit   string
	Offerings  []normalizedOffering
}

type normalizedOffering struct {
	ID string
}

func normalizeConfigCapabilities(in []config.RegistryCapability) []normalizedCapability {
	out := make([]normalizedCapability, 0, len(in))
	for _, capability := range in {
		row := normalizedCapability{
			Capability: capability.Name,
			WorkUnit:   capability.WorkUnit,
			Offerings:  make([]normalizedOffering, 0, len(capability.Offerings)),
		}
		for _, offering := range capability.Offerings {
			row.Offerings = append(row.Offerings, normalizedOffering{
				ID: offering.ID,
			})
		}
		sort.Slice(row.Offerings, func(i, j int) bool {
			return row.Offerings[i].ID < row.Offerings[j].ID
		})
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Capability < out[j].Capability
	})
	return out
}

func normalizeDaemonCapabilities(in []paymentclient.Capability) []normalizedCapability {
	out := make([]normalizedCapability, 0, len(in))
	for _, capability := range in {
		row := normalizedCapability{
			Capability: capability.Capability,
			WorkUnit:   capability.WorkUnit,
			Offerings:  make([]normalizedOffering, 0, len(capability.Offerings)),
		}
		for _, offering := range capability.Offerings {
			row.Offerings = append(row.Offerings, normalizedOffering{
				ID: offering.ID,
			})
		}
		sort.Slice(row.Offerings, func(i, j int) bool {
			return row.Offerings[i].ID < row.Offerings[j].ID
		})
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Capability < out[j].Capability
	})
	return out
}
