package compaction

import (
	"github.com/qomos-w/sporemind/pkg/domain"
)

// Mutable singletons
// TODO(actor-ownership): migrate to actor-owned state

// DefaultCompactionPolicy is the hardcoded global fallback when neither
// instance override nor Kind-level config enables compaction.
var DefaultCompactionPolicy = domain.CompactionPolicy{
	Enabled:          true,
	BudgetMode:       "percentage",
	TokenBudget:      65,
	RecentWindow:     20,
	SummaryUnit:      &domain.ModelUnit{},
	MaxSummaryTokens: 4000,
}
