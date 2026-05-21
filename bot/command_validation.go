package bot

import (
	"fmt"
	"strings"
)

func validatePluginCommandNames(pluginName string, commands, messageMatchers []InputMatcher) error {
	for _, matcher := range commands {
		if isEngineReservedCommandName(matcher.Command) {
			return fmt.Errorf("plugin '%s' command %q is reserved for engine use; plugin command names must not start with '_'", pluginName, matcher.Command)
		}
	}
	for _, matcher := range messageMatchers {
		if isEngineReservedCommandName(matcher.Command) {
			return fmt.Errorf("plugin '%s' message matcher command %q is reserved for engine use; plugin command names must not start with '_'", pluginName, matcher.Command)
		}
	}
	return nil
}

func isEngineReservedCommandName(command string) bool {
	return strings.HasPrefix(strings.TrimSpace(command), "_")
}
