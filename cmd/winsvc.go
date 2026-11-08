package cmd

import (
	"errors"

	"goft/internal/config"
	"goft/internal/i18n"
)

// requireLogFiles refuses a configuration that would log to a console, for a
// service that has none.
//
// A Windows service's stdout goes nowhere: a log left on it would be lost
// without a word, which is worse than not starting. So every log the service
// writes — its own and each enabled job's — has to name a file.
func requireLogFiles(cfg *config.Config) error {
	var errs []error
	if cfg.Service.Log.Path == "" {
		errs = append(errs, i18n.Errorf("service.log.path is not set"))
	}
	for _, j := range cfg.Enabled() {
		if j.Log.Path == "" {
			errs = append(errs, i18n.Errorf("job %s: log.path is not set", j.Name))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return i18n.Errorf("a Windows service has no console to log to, so every log needs a file:\n%w", errors.Join(errs...))
}
