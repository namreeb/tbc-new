import { StatCapType } from '@generated/proto/api';
import { Class, PseudoStat } from '@generated/proto/common';
import { StatCap } from '@sim/proto/stats';
import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ReforgeSoftCapsTooltip } from './ReforgeSoftCapsTooltip';

const player = { getClass: () => Class.ClassPaladin } as any;

describe('ReforgeSoftCapsTooltip', () => {
	// Prot Paladin's only soft cap is ReducedCritTakenPercent, which has no single rating
	// conversion. The post-cap EP cell used to receive null here and throw on .toFixed(), so
	// hovering the Suggest Reforges button took the whole sidebar down.
	it('renders a soft cap whose stat has no percent conversion', () => {
		const softCaps = [
			StatCap.fromPseudoStat(PseudoStat.PseudoStatReducedCritTakenPercent, {
				breakpoints: [5.6],
				capType: StatCapType.TypeSoftCap,
				postCapEPs: [0],
			}),
		];

		const { container } = render(<ReforgeSoftCapsTooltip player={player} softCaps={softCaps} additionalInformation={{}} />);

		expect(container.querySelectorAll('tr').length).toBeGreaterThan(0);
		expect(container.textContent).toContain('0.00');
	});
});
