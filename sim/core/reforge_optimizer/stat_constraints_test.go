//go:build with_db

package reforgeoptimizer

import (
	"testing"

	"github.com/wowsims/tbc/sim"
	"github.com/wowsims/tbc/sim/core"
	"github.com/wowsims/tbc/sim/core/proto"
	"github.com/wowsims/tbc/sim/core/simsignals"
	"github.com/wowsims/tbc/sim/core/stats"
)

func statConstraint(stat proto.Stat, op proto.BulkStatConstraintOp, value float64) *proto.BulkStatConstraint {
	return &proto.BulkStatConstraint{UnitStat: &proto.BulkStatConstraint_Stat{Stat: stat}, Op: op, Value: value}
}

func optimizedFinalStats(t *testing.T, request *proto.ReforgeOptimizeRequest) (core.UnitStats, float64) {
	t.Helper()
	result := Optimize(request)
	if result.GetError() != nil {
		t.Fatalf("Optimize failed: %s", result.GetError().GetMessage())
	}
	return protoToCoreUnitStats(result.GetOptimizedPlayerStats().GetFinalStats()), result.GetScore()
}

// Stat constraints are rows of the model: the solver picks gems that keep the constrained
// stat on the right side of the threshold, reports infeasibility when no gem choice can,
// and decides constraints on stats the gems cannot move on the base stats.
func TestStatConstraintsInModel(t *testing.T) {
	sim.RegisterAll()
	request := loadPreset(t, "gem-pool-wide.test.json")
	request.Debug = false

	optimizer, err := newReforgeOptimizer(request, simsignals.CreateSignals())
	if err != nil {
		t.Fatalf("newReforgeOptimizer: %v", err)
	}
	base := optimizer.capBaseStats
	unconstrained, unconstrainedScore := optimizedFinalStats(t, request)

	// The stat the unconstrained gems raise the most is the one to constrain.
	var target stats.Stat
	var targetDelta float64
	for statIdx := range unconstrained.Stats {
		if delta := unconstrained.Stats[statIdx] - base.Stats[statIdx]; delta > targetDelta {
			target, targetDelta = stats.Stat(statIdx), delta
		}
	}
	if targetDelta <= 0 {
		t.Fatal("unconstrained gems raise no stat; fixture unsuitable")
	}
	targetStat := proto.Stat(target)
	t.Logf("constraining %s: base %.1f, unconstrained %.1f", target.StatName(), base.Stats[target], unconstrained.Stats[target])

	// An upper bound below what the free solve reached: satisfiable with other gems, at a cost.
	bound := base.Stats[target] + targetDelta/2
	request.StatConstraints = []*proto.BulkStatConstraint{statConstraint(targetStat, proto.BulkStatConstraintOp_BulkStatConstraintOpLessThanOrEqual, bound)}
	constrained, constrainedScore := optimizedFinalStats(t, request)
	if constrained.Stats[target] > bound+1e-6 {
		t.Fatalf("%s = %.1f exceeds the constraint bound %.1f", target.StatName(), constrained.Stats[target], bound)
	}
	if constrainedScore > unconstrainedScore+1e-9 {
		t.Fatalf("constrained score %.3f beats the unconstrained optimum %.3f", constrainedScore, unconstrainedScore)
	}

	// A lower bound above anything the gems can reach: infeasible, flagged as such.
	request.StatConstraints = []*proto.BulkStatConstraint{statConstraint(targetStat, proto.BulkStatConstraintOp_BulkStatConstraintOpGreaterThanOrEqual, unconstrained.Stats[target]+10000)}
	result := Optimize(request)
	if !result.GetInfeasibleStatConstraints() || result.GetError() == nil {
		t.Fatalf("unreachable constraint should be reported infeasible, got %+v", result)
	}

	// A lower bound the free solve already clears: the same solution as unconstrained.
	request.StatConstraints = []*proto.BulkStatConstraint{statConstraint(targetStat, proto.BulkStatConstraintOp_BulkStatConstraintOpGreaterThan, base.Stats[target])}
	loose, looseScore := optimizedFinalStats(t, request)
	if loose.Stats[target] != unconstrained.Stats[target] || looseScore != unconstrainedScore {
		t.Fatalf("a constraint the optimum satisfies must not change it: %.1f/%.3f vs %.1f/%.3f", loose.Stats[target], looseScore, unconstrained.Stats[target], unconstrainedScore)
	}

	// Frost resistance is carried by no gem here, so it is decided on the base stats.
	frostRes := base.Stats[stats.FrostResistance]
	request.StatConstraints = []*proto.BulkStatConstraint{statConstraint(proto.Stat_StatFrostResistance, proto.BulkStatConstraintOp_BulkStatConstraintOpGreaterThanOrEqual, frostRes)}
	if result := Optimize(request); result.GetError() != nil {
		t.Fatalf("a base-satisfied constraint on an unmovable stat must solve: %s", result.GetError().GetMessage())
	}
	request.StatConstraints = []*proto.BulkStatConstraint{statConstraint(proto.Stat_StatFrostResistance, proto.BulkStatConstraintOp_BulkStatConstraintOpGreaterThan, frostRes)}
	if result := Optimize(request); !result.GetInfeasibleStatConstraints() {
		t.Fatalf("a base-failed constraint on an unmovable stat must be infeasible, got %+v", result)
	}
}
