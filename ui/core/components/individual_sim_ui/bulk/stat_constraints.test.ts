import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { BulkSettings, BulkStatConstraint, BulkStatConstraintOp } from '../../../proto/api';
import { PseudoStat, Stat, UnitStats } from '../../../proto/common';
import { constraintStatValue, finalStatsPassConstraints, newStatConstraint, statConstraintPasses } from './stat_constraints';

const Op = BulkStatConstraintOp;

const statConstraint = (stat: Stat, op: BulkStatConstraintOp, value: number) => BulkStatConstraint.create({ unitStat: { oneofKind: 'stat', stat }, op, value });

const pseudoStatConstraint = (pseudoStat: PseudoStat, op: BulkStatConstraintOp, value: number) =>
	BulkStatConstraint.create({ unitStat: { oneofKind: 'pseudoStat', pseudoStat }, op, value });

const finalStats = (stats: Partial<Record<Stat, number>> = {}, pseudoStats: Partial<Record<PseudoStat, number>> = {}): UnitStats => {
	const proto = UnitStats.create({
		stats: new Array(Stat.StatPhysicalDamage + 1).fill(0),
		pseudoStats: new Array(PseudoStat.PseudoStatReducedCritTakenPercent + 1).fill(0),
	});
	for (const [stat, value] of Object.entries(stats)) proto.stats[Number(stat)] = value;
	for (const [pseudoStat, value] of Object.entries(pseudoStats)) proto.pseudoStats[Number(pseudoStat)] = value;
	return proto;
};

describe('statConstraintPasses', () => {
	const cases: Array<[BulkStatConstraintOp, number, boolean]> = [
		[Op.BulkStatConstraintOpGreaterThan, 176, true],
		[Op.BulkStatConstraintOpGreaterThan, 175, false],
		[Op.BulkStatConstraintOpGreaterThanOrEqual, 175, true],
		[Op.BulkStatConstraintOpGreaterThanOrEqual, 174, false],
		[Op.BulkStatConstraintOpEqual, 175, true],
		[Op.BulkStatConstraintOpEqual, 174, false],
		[Op.BulkStatConstraintOpLessThanOrEqual, 175, true],
		[Op.BulkStatConstraintOpLessThanOrEqual, 176, false],
		[Op.BulkStatConstraintOpLessThan, 174, true],
		[Op.BulkStatConstraintOpLessThan, 175, false],
	];

	for (const [op, value, expected] of cases) {
		it(`${Op[op]} with threshold 175 and value ${value} -> ${expected}`, () => {
			assert.equal(statConstraintPasses(statConstraint(Stat.StatFireResistance, op, 175), value), expected);
		});
	}
});

describe('constraintStatValue', () => {
	it('reads a Stat from the stats array', () => {
		const constraint = statConstraint(Stat.StatFireResistance, Op.BulkStatConstraintOpGreaterThan, 0);
		assert.equal(constraintStatValue(constraint, finalStats({ [Stat.StatFireResistance]: 93 })), 93);
	});

	it('reads a PseudoStat from the pseudoStats array', () => {
		const constraint = pseudoStatConstraint(PseudoStat.PseudoStatReducedCritTakenPercent, Op.BulkStatConstraintOpGreaterThanOrEqual, 5.6);
		assert.equal(constraintStatValue(constraint, finalStats({}, { [PseudoStat.PseudoStatReducedCritTakenPercent]: 5.98 })), 5.98);
	});

	it('reads 0 for a missing value or an unset target', () => {
		assert.equal(constraintStatValue(statConstraint(Stat.StatFireResistance, Op.BulkStatConstraintOpGreaterThan, 0), UnitStats.create()), 0);
		assert.equal(constraintStatValue(BulkStatConstraint.create(), finalStats({ [Stat.StatStamina]: 100 })), 0);
	});

	it('decodes rows saved before the target became a oneof', () => {
		const settings = BulkSettings.fromJsonString('{"statConstraints":[{"stat":37,"value":85}]}', { ignoreUnknownFields: true });
		const [constraint] = settings.statConstraints;
		assert.deepEqual(constraint.unitStat, { oneofKind: 'stat', stat: Stat.StatFireResistance });
		assert.equal(constraint.op, Op.BulkStatConstraintOpGreaterThan);
		assert.equal(constraintStatValue(constraint, finalStats({ [Stat.StatFireResistance]: 93 })), 93);
	});

	it('round-trips a PseudoStat row through JSON', () => {
		const original = pseudoStatConstraint(PseudoStat.PseudoStatReducedCritTakenPercent, Op.BulkStatConstraintOpGreaterThanOrEqual, 5.6);
		const decoded = BulkStatConstraint.fromJsonString(BulkStatConstraint.toJsonString(original, { enumAsInteger: true }));
		assert.deepEqual(decoded, original);
	});
});

describe('finalStatsPassConstraints', () => {
	it('requires every constraint to pass', () => {
		const constraints = [
			statConstraint(Stat.StatFireResistance, Op.BulkStatConstraintOpGreaterThan, 175),
			pseudoStatConstraint(PseudoStat.PseudoStatReducedCritTakenPercent, Op.BulkStatConstraintOpGreaterThanOrEqual, 5.6),
		];
		const critImmune = { [PseudoStat.PseudoStatReducedCritTakenPercent]: 5.6 };
		assert.equal(finalStatsPassConstraints(constraints, finalStats({ [Stat.StatFireResistance]: 200 }, critImmune)), true);
		assert.equal(finalStatsPassConstraints(constraints, finalStats({ [Stat.StatFireResistance]: 175 }, critImmune)), false);
		assert.equal(
			finalStatsPassConstraints(constraints, finalStats({ [Stat.StatFireResistance]: 200 }, { [PseudoStat.PseudoStatReducedCritTakenPercent]: 5.2 })),
			false,
		);
	});

	it('passes with no constraints', () => {
		assert.equal(finalStatsPassConstraints([], UnitStats.create()), true);
	});

	it('creates a new constraint that any gear set passes', () => {
		assert.equal(finalStatsPassConstraints([newStatConstraint()], UnitStats.create()), true);
	});
});
