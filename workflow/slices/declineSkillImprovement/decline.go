// Package declineskillimprovement notices, at a session's startup, the
// applied skill improvements the owner reverted while keeping their record
// lines: each counts as declined, once, in the skill's record and in the
// repository's history. It is run by the SessionStart hook, never by an
// agent.
package declineskillimprovement

import (
	"errors"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/pluginskills"
)

// Decline records as declined every applied improvement of the plugin
// directory's skills that its skill is back from. The records are written
// first, so a crash before the history never declines twice.
func Decline(pluginRoot string, store *eventlog.Store, actor eventlog.Actor) ([]domain.Event, error) {
	local, err := pluginskills.Local(pluginRoot)
	if err != nil {
		return nil, err
	}
	var declined []domain.Event
	var errs []error
	for _, skill := range local {
		if skill.Vendored || len(skill.Record) == 0 {
			continue
		}
		current, err := pluginskills.Version(pluginRoot, skill.Name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		events, err := pluginskills.Change(pluginRoot, skill.Name, false, func(s domain.PluginSkill) ([]domain.Event, error) {
			return domain.DeclineSkillImprovement(s, current), nil
		})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		declined = append(declined, events...)
	}
	if err := pluginskills.AppendToHistory(store, actor, declined); err != nil {
		errs = append(errs, err)
	}
	return declined, errors.Join(errs...)
}
