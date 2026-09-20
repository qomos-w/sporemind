package agent

import "github.com/qomos-w/sporemind/pkg/domain"

// seedTestBuiltinMounts pre-populates ComponentMounts with the builtin
// protected component cards so tests that exercise resolveTools /
// resolveInstructions / resolveComponentSnapshot have the same starting
// state as a real agent after OnStart → seedBuiltinComponentMounts.
func seedTestBuiltinMounts(a *Actor, debugEnabled bool) {
	cards := []string{
		"builtin:bundle:project-wiki",
	}
	if debugEnabled {
		cards = append(cards, "builtin:bundle:debug")
	}
	for _, cardID := range cards {
		a.ComponentMounts = append(a.ComponentMounts, domain.AgentComponentMount{
			CardID:  cardID,
			Enabled: true,
			Scope:   "builtin",
		})
	}
}
