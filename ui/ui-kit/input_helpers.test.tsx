import type { Player } from '@sim/player/player';
import { ActionId } from '@sim/proto/action_id';
import type { StoreSubscribe } from '@sim/state/subscriptions';
import { fireEvent, render, screen } from '@testing-library/react';
import { PickerShell } from '@ui-kit/PickerShell';
import { describe, expect, it } from 'vitest';

import {
	makeBooleanIconInput,
	makeClassOptionsBooleanIconInput,
	makeClassOptionsEnumIconInput,
	makeQuadstateIconInput,
	makeRotationEnumIconInput,
	makeSpecOptionsBooleanIconInput,
	makeSpecOptionsEnumIconInput,
} from './input_helpers';

// The factories are generic over the message they write into, so a synthetic one keeps these tests
// off the live buff protos: a numeric level plus the boolean a quadstate input keeps its fourth
// state in.
type Message = { level: number; flag: boolean };

const stub: StoreSubscribe = () => () => {};

const quadstate = (extra: { showWhen?: (modObj: Message) => boolean } = {}) =>
	makeQuadstateIconInput<any, Message, Message>(
		{
			getModObject: (modObj: any) => modObj as Message,
			getValue: (modObj: Message) => modObj,
			setValue: (modObj: Message, newVal: Message) => Object.assign(modObj, newVal),
			storeSubscribe: () => stub,
			...extra,
		},
		ActionId.fromSpellId(1),
		ActionId.fromSpellId(2),
		ActionId.fromItemId(3),
		'level',
		'flag',
	);

describe('makeQuadstateIconInput', () => {
	it('spreads its four states across the level field and the second improved flag', () => {
		const message: Message = { level: 0, flag: false };
		const input = quadstate();
		const player = message as unknown as Player<any>;

		expect(input.states).toBe(4);

		const roundTrip = [0, 1, 2, 3].map(value => {
			input.setValue(player, value);
			return [message.level, message.flag, input.getValue(player)];
		});

		expect(roundTrip).toEqual([
			[0, false, 0],
			[1, false, 1],
			[2, false, 2],
			[2, true, 3],
		]);
	});

	// Every tristate, quadstate and multistate buff factory routes through makeNumberIconInput, so a
	// predicate it drops takes the faction gate on all of them with it.
	it('keeps showWhen, which the picker hides on', () => {
		const player = { level: 0, flag: false } as unknown as Player<any>;
		const seen: Message[] = [];

		expect(quadstate({ showWhen: modObj => (seen.push(modObj), false) }).showWhen!(player)).toBe(false);
		expect(seen).toEqual([player]);
		expect(quadstate({ showWhen: () => true }).showWhen!(player)).toBe(true);
		expect(quadstate().showWhen!(player)).toBe(true);
	});
});

// The picker is handed the player, while a buff config is written against the party or raid it
// reaches through, so every predicate has to be mapped on the way out or it can never fire.
describe('makeBooleanIconInput', () => {
	type Party = { buffs: Message; leaderPresent: boolean };

	const partyInput = (enableWhen?: (party: Party) => boolean) =>
		makeBooleanIconInput<any, Message, Party>(
			{
				getModObject: (player: Player<any>) => (player as unknown as { party: Party }).party,
				getValue: modObj => modObj.buffs,
				setValue: (modObj, newVal) => Object.assign(modObj.buffs, newVal),
				storeSubscribe: () => stub,
				enableWhen,
			},
			ActionId.fromSpellId(1),
			'flag',
		);

	it('maps enableWhen onto the mod object the config was written against', () => {
		const party: Party = { buffs: { level: 0, flag: false }, leaderPresent: false };
		const player = { party } as unknown as Player<any>;
		const input = partyInput(modObj => modObj.leaderPresent);

		expect(input.enableWhen!(player)).toBe(false);
		party.leaderPresent = true;
		expect(input.enableWhen!(player)).toBe(true);
	});

	it('leaves enableWhen unset when the config names none, so the picker stays enabled', () => {
		expect(partyInput().enableWhen).toBeUndefined();
	});
});

// A spec-level icon input's label and its tooltip are rendered only by PickerShell, so a factory that
// drops either leaves no type error behind — the picker simply comes out unlabelled.
describe('icon input factories', () => {
	const label = 'Primary option';
	const labelTooltip = 'What the primary option controls.';
	const chrome = { label, labelTooltip };

	const factories: Record<string, () => { label?: string; labelTooltip?: unknown }> = {
		makeClassOptionsBooleanIconInput: () =>
			makeClassOptionsBooleanIconInput<any>({ ...chrome, fieldName: 'primary' as never, id: ActionId.fromSpellId(1) }),
		makeSpecOptionsBooleanIconInput: () => makeSpecOptionsBooleanIconInput<any>({ ...chrome, fieldName: 'primary' as never, id: ActionId.fromSpellId(1) }),
		makeClassOptionsEnumIconInput: () => makeClassOptionsEnumIconInput<any, number>({ ...chrome, fieldName: 'primary' as never, values: [] }),
		makeSpecOptionsEnumIconInput: () => makeSpecOptionsEnumIconInput<any, number>({ ...chrome, fieldName: 'primary' as never, values: [] }),
		makeRotationEnumIconInput: () => makeRotationEnumIconInput<any, number>({ ...chrome, fieldName: 'primary' as never, values: [] }),
	};

	// react-tooltip resolves anchors document-wide by id, so two pickers sharing one leaves every
	// labelled icon input on a tab showing all of its neighbours' tooltips at once.
	it('gives each picker a tooltip of its own', async () => {
		const other = makeClassOptionsEnumIconInput<any, number>({
			label: 'Secondary option',
			labelTooltip: 'What the secondary option controls.',
			fieldName: 'secondary' as never,
			values: [],
		});
		render(
			<>
				<PickerShell config={factories.makeRotationEnumIconInput() as any} className="ui-icon-field" hidden={false} disabled={false} />
				<PickerShell config={other as any} className="ui-icon-field" hidden={false} disabled={false} />
			</>,
		);

		fireEvent.mouseEnter(screen.getByText(label));
		expect(await screen.findByText(labelTooltip)).toBeTruthy();
		expect(screen.queryByText('What the secondary option controls.')).toBeNull();
	});

	it.each(Object.keys(factories))('carries a label and its tooltip through %s into the shell', async name => {
		const config = factories[name]();
		render(<PickerShell config={{ ...config, id: 'icon-input' } as any} className="ui-icon-field" hidden={false} disabled={false} />);

		expect(screen.getByTestId('form-label').textContent).toBe(label);
		fireEvent.mouseEnter(screen.getByText(label));
		expect(await screen.findByText(labelTooltip)).toBeTruthy();
	});
});
