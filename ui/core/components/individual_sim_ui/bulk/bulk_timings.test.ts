import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { BulkSimTimings, formatDurationMs } from './bulk_timings';

// A controllable clock: tick() advances time by the given amount.
const fakeClock = () => {
	let t = 0;
	return { now: () => t, tick: (ms: number) => (t += ms) };
};

describe('BulkSimTimings', () => {
	it('accumulates phase durations and counts across repeated phases', () => {
		const clock = fakeClock();
		const timings = new BulkSimTimings(500, clock.now);
		timings.start();
		timings.startPhase('build');
		clock.tick(120);
		timings.endPhase('build', 10);
		timings.startPhase('build');
		clock.tick(30);
		timings.endPhase('build', 5);
		clock.tick(1000);
		timings.finish();
		const report = timings.report();
		assert.deepEqual(report.phases.build, { ms: 150, count: 15 });
		assert.deepEqual(report.phases.gems, { ms: 0, count: 0 });
		assert.equal(report.totalMs, 1150);
	});

	it('ignores ending a phase that was never started', () => {
		const timings = new BulkSimTimings(500, fakeClock().now);
		timings.endPhase('gems', 3);
		assert.deepEqual(timings.report().phases.gems, { ms: 0, count: 0 });
	});

	it('returns a snapshot that does not alias internal state', () => {
		const timings = new BulkSimTimings(500, fakeClock().now);
		const report = timings.report();
		report.phases.build.count = 99;
		assert.equal(timings.report().phases.build.count, 0);
	});
});

describe('formatDurationMs', () => {
	it('picks units by magnitude', () => {
		assert.equal(formatDurationMs(42.4), '42 ms');
		assert.equal(formatDurationMs(1500), '1.50 s');
		assert.equal(formatDurationMs(125_300), '2 min 5.3 s');
	});
});
