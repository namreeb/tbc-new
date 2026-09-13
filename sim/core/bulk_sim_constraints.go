package core

import "github.com/wowsims/tbc/sim/core/proto"

// Stat constraint evaluation for the batch sim, against a character's final
// stats. Mirrors ui/core/components/individual_sim_ui/bulk/stat_constraints.ts.

func bulkStatConstraintPasses(constraint *proto.BulkStatConstraint, value float64) bool {
	switch constraint.Op {
	case proto.BulkStatConstraintOp_BulkStatConstraintOpGreaterThan:
		return value > constraint.Value
	case proto.BulkStatConstraintOp_BulkStatConstraintOpGreaterThanOrEqual:
		return value >= constraint.Value
	case proto.BulkStatConstraintOp_BulkStatConstraintOpEqual:
		return value == constraint.Value
	case proto.BulkStatConstraintOp_BulkStatConstraintOpLessThanOrEqual:
		return value <= constraint.Value
	case proto.BulkStatConstraintOp_BulkStatConstraintOpLessThan:
		return value < constraint.Value
	}
	return false
}

// The constrained Stat or PseudoStat read out of a final-stats proto. A
// constraint with no target reads as 0, as in the UI.
func bulkConstraintStatValue(constraint *proto.BulkStatConstraint, finalStats *proto.UnitStats) float64 {
	if finalStats == nil {
		return 0
	}
	switch target := constraint.UnitStat.(type) {
	case *proto.BulkStatConstraint_Stat:
		if idx := int(target.Stat); idx < len(finalStats.Stats) {
			return finalStats.Stats[idx]
		}
	case *proto.BulkStatConstraint_PseudoStat:
		if idx := int(target.PseudoStat); idx < len(finalStats.PseudoStats) {
			return finalStats.PseudoStats[idx]
		}
	}
	return 0
}

func bulkFinalStatsPassConstraints(constraints []*proto.BulkStatConstraint, finalStats *proto.UnitStats) bool {
	for _, constraint := range constraints {
		if !bulkStatConstraintPasses(constraint, bulkConstraintStatValue(constraint, finalStats)) {
			return false
		}
	}
	return true
}
