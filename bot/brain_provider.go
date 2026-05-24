package bot

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/lnxjedi/gopherbot/robot"
)

func initializeConfiguredBrain() (robot.SimpleBrain, string, error) {
	provider := currentCfg.brainProvider
	if provider == "" {
		provider = "mem"
	}
	if provider == "file" {
		if _, err := os.Stat(filepath.Join(currentCfg.brainCache.Directory, "control.json")); os.IsNotExist(err) {
			if legacyPath, ok := legacyFileBrainDataPresent(); ok {
				return nil, provider, fmt.Errorf("legacy file brain data found in %s; run gopherbot pull-brain", legacyPath)
			}
		}
		brain, err := newLocalCachedBrain(currentCfg.brainCache)
		if err != nil {
			return nil, provider, err
		}
		return brain, provider, nil
	}
	registration, ok := brainProviderRegistration(provider)
	if !ok {
		return nil, provider, fmt.Errorf("no provider registered for brain: %q", provider)
	}
	if registration.RemoteProvider != nil {
		remote := registration.RemoteProvider(handle)
		if remote == nil {
			return nil, provider, fmt.Errorf("remote brain provider %q returned nil", provider)
		}
		brain, err := newRemoteCachedBrain(currentCfg.brainCache, remote)
		if err != nil {
			return nil, provider, err
		}
		return brain, provider, nil
	}
	if registration.Provider != nil {
		return registration.Provider(handle), provider, nil
	}
	return nil, provider, fmt.Errorf("brain provider %q has no usable provider function", provider)
}

func legacyFileBrainDataPresent() (string, bool) {
	var cfg struct {
		BrainDirectory string
	}
	_ = handle.GetBrainConfig(&cfg)
	if cfg.BrainDirectory == "" {
		return "", false
	}
	if filepath.Clean(cfg.BrainDirectory) == filepath.Clean(currentCfg.brainCache.Directory) {
		return "", false
	}
	entries, err := os.ReadDir(cfg.BrainDirectory)
	if err != nil {
		return "", false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return cfg.BrainDirectory, true
		}
	}
	return "", false
}
