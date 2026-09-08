'use client'

/* eslint-disable react/no-unescaped-entities, react/jsx-key --
 * Guide prose ported from the console (draft-formance-poc components/guide/v3):
 * quote characters render as-is, and GuideTable row-cell nodes render inside
 * keyed <td> elements (no missing-key at runtime). Both rules are noise here. */

// The reconciliation ("Ledger Clarity") User Guide.
//
// Ported verbatim from the console shell's reconcile guide sections
// (draft-formance-poc apps/web/components/guide/v3/content.tsx) when
// reconciliation was retrofitted as a standalone, self-serving module — only the
// presentation primitives were translated (Section/KeyValueTable/link/h3 →
// Section/GuideTable/AppLink/SubHeading). Rendered by ./guide-shell.

import * as React from 'react'
import { Scale, ListChecks, History, Bell, LineChart, ShieldCheck } from 'lucide-react'
import { Section, SubHeading, P, Callout, GuideTable, AppLink, Code } from './primitives'

export interface GuideSection {
	id: string
	group: string
	label: string
	icon: React.ReactNode
	render: () => React.ReactNode
}

export const DEFAULT_SECTION = 'overview'

export const GUIDE_SECTIONS: GuideSection[] = [
	{
		id: 'overview',
		group: 'Reconciliation',
		label: 'Reconcile',
		icon: <Scale className="size-4" aria-hidden />,
		render: () => (
			<Section
				id="overview"
				eyebrow="Reconciliation"
				title="Reconcile"
				intro="Reconcile (“Ledger Clarity”) watches the balances and relationships that must always hold across your ledgers, records every check it runs, flags the ones that break, and helps you resolve or accept each break. It is a business process (observe, detect, resolve), not only a rules engine."
			>
				<SubHeading id="v3-rec-where">Where it lives</SubHeading>
				<P>
					Open <strong>Reconcile</strong>{' '}from the sidebar. Unlike the ledger explorer, it is not tied to
					the ledger you are currently exploring: a reconciliation rule names its own ledgers, so it can
					compare accounts <em>within</em> one ledger or <em>across</em> several. It talks to its own
					reconciliation service. A status pill in the top-right shows whether that service is reachable.
					Click it to point the UI at a different recon instance (for example, one wired to a staging
					ledger).
				</P>

				<SubHeading id="v3-rec-process">Observe → detect → resolve</SubHeading>
				<GuideTable
					caption="The reconciliation process"
					columns={['Step', 'What happens', 'Where']}
					rows={[
						[<strong key="observe">Observe</strong>, 'You define rules: the invariants that must hold on your ledgers', <span key="rules-link"><AppLink href="/guide?section=rules">Rules & templates</AppLink></span>],
						[<strong key="detect">Detect</strong>, 'Each evaluation records a PASS, FAIL, or ERROR receipt and, on a failure, may open an alert (a break)', <span key="captures-link"><AppLink href="/guide?section=activity">Rule activity</AppLink></span>],
						[<strong key="resolve">Resolve</strong>, 'A human works each break: acknowledge, resolve, accept, or snooze it', <span key="alerts-link"><AppLink href="/guide?section=alerts">Alerts & resolution</AppLink></span>],
					]}
				/>

				<SubHeading id="v3-rec-tabs">The five tabs</SubHeading>
				<GuideTable
					caption="Reconcile tabs"
					columns={['Tab', 'Purpose']}
					rows={[
						[<strong key="overview">Overview</strong>, 'Clickable shortcuts to open alerts, handled alerts, rules, and the most recent break activity'],
						[<strong key="rules">Rules</strong>, <span key="rules-purpose">The checks you define, with an enable toggle and one-click evaluation (see <AppLink href="/guide?section=rules">Rules & templates</AppLink>)</span>],
						[<strong key="alerts">Alerts</strong>, <span key="alerts-purpose">The inbox of breaks to triage and resolve (see <AppLink href="/guide?section=alerts">Alerts & resolution</AppLink>)</span>],
						[<strong key="insights">Insights</strong>, <span key="insights-purpose">Deviation and break distribution built from reconciliation history (see <AppLink href="/guide?section=insights">Insights</AppLink>)</span>],
						[<strong key="audit">Audit</strong>, <span key="audit-purpose">The signed, independently verifiable record of what Reconcile did, and who did it (see <AppLink href="/guide?section=audit">Audit & verification</AppLink>)</span>],
					]}
				/>

				<SubHeading id="v3-rec-first-run">A practical first reconciliation</SubHeading>
				<ol className="list-decimal space-y-1 pl-7 text-sm">
					<li>Open <strong>Rules</strong>, select <strong>New rule</strong>, and choose the template card that describes the control you need.</li>
					<li>Use <strong>Load example</strong>{' '}to see a complete configuration, then replace the sample ledgers, account selectors, assets, and amounts.</li>
					<li>Choose the <strong>alert grouping period</strong>{' '}and <strong>run mode</strong>. These answer different questions: how failures share an alert, and what triggers an evaluation.</li>
					<li>Confirm each account set or source matches exactly what you expect before creating the rule.</li>
					<li>Turn monitoring <strong>On</strong>{' '}and select <strong>Run now</strong>. The latest result appears in the rule header.</li>
					<li>If it fails, select the result to open the alert, inspect the evidence, and record the operational decision.</li>
				</ol>

				<Callout kind="tip" title="Navigation behaves like the rest of the app">
					Rule names and alert fingerprints open their detail screens. The in-page back arrow returns to the
					list, and the browser Back button also restores the previous Reconcile tab, filter, or detail view.
				</Callout>

				<Callout kind="info" title="Human-first by design">
					Reconcile never silently &quot;fixes&quot; your books. It surfaces a break with its evidence and
					leaves the decision (correct it, accept it, or wait) to a person, keeping an auditable record of
					who did what and why.
				</Callout>
			</Section>
		),
	},
	{
		id: 'rules',
		group: 'Reconciliation',
		label: 'Rules & templates',
		icon: <ListChecks className="size-4" aria-hidden />,
		render: () => (
			<Section
				id="rules"
				eyebrow="Reconciliation"
				title="Rules & templates"
				intro="A rule is an operational control you want to keep true. The Rules tab shows each control as a distinct card: its human-readable comparison, latest result, monitoring state, and Run now action. Select the card header to open its current configuration and combined activity timeline."
			>
				<SubHeading id="v3-rec-templates">Templates</SubHeading>
				<GuideTable
					caption="Rule templates"
					columns={['Template', 'Use it to…', 'Typical setup']}
					rows={[
						[<strong key="equation">Balance equation</strong>, 'prove that a signed set of balances nets to zero', 'Two to thirty-two named sources, each with a sign, and a tolerance per asset'],
						[<strong key="rate">Exchange-rate bounds</strong>, 'prove a derived rate stays inside a band', 'A base source and a quote source, with inclusive rate bounds'],
						[<strong key="consensus">Source consensus</strong>, 'prove independently sourced balances agree', 'Every named source must report, and the widest spread must stay within tolerance'],
						[<strong key="coverage">Coverage-ratio bounds</strong>, 'prove one portfolio covers another', 'A numerator and a denominator portfolio, with inclusive ratio bounds'],
						[<strong key="bounds">Balance bounds</strong>, 'detect when one account set leaves an allowed range', 'A minimum reserve, a maximum exposure, or both, per asset'],
						[<strong key="stale">Stale holds</strong>, 'catch held funds that outstay their deadline', 'A set of hold accounts plus the metadata key carrying each deadline, with a fallback maximum age'],
					]}
				/>

				<SubHeading id="v3-rec-create">Creating a rule</SubHeading>
				<P>
					Click <strong>New rule</strong>. The template cards explain the control before you select it, and
					the form then adapts to it: signed named sources for an equation, one account set with limits for
					bounds, a base and a quote for a rate, a hold set and a deadline key for stale holds.{' '}
					<strong>Load example</strong>{' '}fills in a complete configuration you can inspect and replace.
					Give the rule an operational name and choose a severity that reflects the response it should
					trigger.
				</P>
				<ul className="list-disc space-y-1 pl-5 text-sm">
					<li><strong>Amounts are integers in the asset&apos;s minor units</strong> (for example, cents). A tolerance of <Code>0</Code> means an exact match.</li>
					<li><strong>Accounts are chosen by an address selector.</strong>{' '}A trailing <Code>*</Code> matches by prefix (<Code>assets:cash:*</Code>); without it the address matches exactly.</li>
					<li><strong>Metadata conditions</strong>{' '}narrow a source further: pick an indexed account-metadata key and an operator suited to its type. Use <em>equals</em> / <em>exists</em> for text and booleans, plus <em>&lt;</em> <em>≤</em> <em>&gt;</em> <em>≥</em> <em>between</em> for numbers and datetimes. Indexed keys are offered live, read through the reconciliation service&apos;s ledger connection.</li>
				</ul>

				<SubHeading id="v3-rec-named-sources">Name your sources instead of ordering them</SubHeading>
				<P>
					Every template reads <strong>named sources</strong>. A source is a name you choose, a ledger, an
					account selector, and the one asset it declares. Configure each by answering three questions:{' '}
					<strong>which ledger</strong>, <strong>which accounts</strong>, and <strong>which value to
					read</strong>. A posting-derived source uses the balance Formance calculates from postings. An
					account-metadata source reads an integer already stored on the account object; it does not
					calculate a balance from postings, and it is aggregate-only.
				</P>
				<P>
					The name is what the rule refers to — an equation&apos;s signs, a rate&apos;s base and quote, a
					ratio&apos;s numerator and denominator all point at names, so reordering the list changes nothing
					and adding a third source is not a different template. Generated CEL stays available under{' '}
					<strong>Technical expression</strong>, but it is a secondary implementation detail.
				</P>
				<GuideTable
					caption="Reading a rule's sources"
					columns={['UI element', 'What it tells you']}
					rows={[
						[<strong key="source-card">Source card</strong>, 'The source name, its ledger, the account selector, and the one asset it contributes'],
						[<strong key="sign">Sign</strong>, 'For an equation, which side of zero this source is added on — give debit-normal sets one sign and credit-normal sets the other, and equal balances on opposite sides cancel'],
						[<strong key="exact">= Exact match</strong>, 'Every configured tolerance is zero; any difference opens a break'],
						[<strong key="tolerated">≈ Within tolerance</strong>, 'At least one non-zero tolerance is allowed; a difference opens a break only outside that band'],
						[<strong key="asset">Asset badge</strong>, 'The single asset this source declares — or every asset the set holds, which fans the rule out to one result per asset'],
						[<strong key="tolerance">Tolerance badge</strong>, 'The allowed deviation for each asset, expressed in that asset’s minor units'],
						[<strong key="mapping-warning">Asset mapping warning</strong>, 'A metadata key represents a different asset from the configured tolerance; review the mapping before relying on the rule'],
					]}
				/>
				<P>
					For a metadata source, choose <strong>one metadata key</strong>{' '}and independently state the{' '}
					<strong>one asset</strong>{' '}its integer represents, such as{' '}
					<Code>account.metadata[&quot;value_known.toto&quot;]</Code> → <Code>USD/2</Code>. The key may
					contain the asset name, as in <Code>reported.USD</Code>, but it does not have to. Other assets may
					exist on the selected account; the source reads only the declared one. The account-selection
					metadata filters above remain separate: they choose accounts, while this key supplies the value
					being reconciled.
				</P>
				<P>
					Choose <strong>one metadata key</strong>{' '}and independently state the <strong>one asset</strong>{' '}
					represented by its integer value, such as <Code>account.metadata[&quot;value_known.toto&quot;]</Code> →{' '}
					<Code>USD/2</Code>. The key may contain the asset name, as in <Code>reported.USD</Code>, but it does
					not have to. Other assets may exist on the selected account; this rule checks only the declared
					asset. Configure another rule for each additional metadata-key/asset pair. The account-selection
					metadata filters above remain separate: they choose accounts, while this key supplies the value
					being reconciled. Generated CEL remains available under <strong>Technical expression</strong>, but
					it is a secondary implementation detail.
				</P>

				<SubHeading id="v3-rec-balance-bounds">Configure balance bounds</SubHeading>
				<P>
					Give one account set a minimum, a maximum, or both, per asset, in that asset&apos;s minor units. A
					rule checks the <em>sum</em>{' '}of the set it selects, so a reserve floor across many accounts is
					one rule rather than one per account.
				</P>
				<Callout kind="info" title="The assets you list are the assets checked">
					Bounds are the one place the asset list is <strong>declared</strong>{' '}rather than discovered from
					what the accounts hold, and that is deliberate. A floor has to keep failing when a set drains to
					nothing — if the asset universe came from what the set currently holds, an account emptied to zero
					would leave the universe and its alert would resolve itself at exactly the moment the money left.
					Adding an asset to the bounds table is what starts checking it.
				</Callout>

				<SubHeading id="v3-rec-stale-holds">Watch for holds that overstay</SubHeading>
				<P>
					A hold ties up money that isn’t spent yet — money in escrow, a reserve against a pending settlement,
					a deposit awaiting release. The <strong>stale holds</strong>{' '}template watches the clock on
					those, so funds can’t sit trapped past the point the hold was meant to be released. It reads each hold’s
					deadline from the account’s own metadata: an expiry recorded on the hold, when it
					carries one, or otherwise the moment the hold was placed plus a maximum age you set (48 hours, say).
				</P>
				<ul className="list-disc space-y-1 pl-5 text-sm">
					<li><strong>One alert per asset, not per hold.</strong>{' '}A rule opens a single alert carrying how many holds are stale, how much they hold in total, and the oldest deadline. It resolves on the next run once nothing is stale. One alert per hold would page you once per problem, which is the wrong shape when a systemic failure strands thousands at once.</li>
					<li><strong>Point a rule at the set you want to watch.</strong>{' '}Because the alert is a total, the rule&apos;s selector is what makes it meaningful: an address prefix plus whatever narrows it to one desk or book. Separate rules over separate sets give you separate alerts — and separate severities.</li>
					<li><strong>Warn before the deadline, not after.</strong>{' '}A rule in <em>approaching</em>{' '}mode flags holds due within a window you choose — the next six hours, for example. Pair it with a second rule in <em>stale</em>{' '}mode at a higher severity: the early warning resolves itself as the breach alert opens, so one hold never leaves two live alerts behind.</li>
					<li><strong>Set the warning window wider than the run interval.</strong>{' '}A rule that runs hourly with a thirty-minute warning window can step straight over the warning and report the breach.</li>
					<li><strong>Finding the holds behind an alert.</strong>{' '}The alert carries the exact query it ran, deadline cutoff included, rather than a list that would grow with the size of the problem. Use <strong>List them</strong>{' '}on the alert to run that query and see the accounts. It answers &ldquo;still past that cutoff and still holding funds&rdquo; — balances are always read live, so it is the current set rather than a snapshot of the run, and the panel says so when the live counts have drifted from the ones the run recorded.</li>
					<li><strong>Released holds keep their metadata.</strong>{' '}Releasing a hold empties the account but leaves the expiry behind, so give the rule a selector that matches live holds only — an address prefix plus something like <Code>metadata[&quot;hold_status&quot;] = active</Code> — rather than every hold ever placed.</li>
				</ul>
				<Callout kind="info" title="The deadline key has to be indexed">
					The service asks the ledger to do the date comparison, so the expiry key must be an indexed
					datetime or integer field on the accounts. A key that isn’t indexed is rejected when you save
					the rule, naming the key — not left to fail at the next evaluation.
				</Callout>
				<Callout kind="tip" title="Only indexed date keys are offered">
					The deadline pickers list the ledger’s indexed datetime and integer keys, because the date
					comparison is pushed down to the ledger — a key the ledger cannot filter on cannot carry a
					deadline.
				</Callout>
				<GuideTable
					caption="Reading a stale-holds alert"
					columns={['Number', 'What it counts']}
					rows={[
						[<strong key="matched">Holds matched</strong>, 'What the ledger returned for the rule’s selector plus the deadline cutoff. Already narrowed to holds past their deadline — and the cost of the run, which is why a rule warns when this reaches its hold limit'],
						[<strong key="released">Released, ignored</strong>, 'Matched accounts holding nothing. Releasing a hold empties the account but leaves its deadline metadata, and a balance cannot be filtered on in the ledger, so these are dropped after the read. A number that keeps growing is dead holds accumulating in the set'],
						[<strong key="flagged">Holds flagged</strong>, 'Still holding funds, and past the deadline. The verdict: the amount held and the oldest deadline describe exactly these, and zero flagged is what makes the run pass'],
					]}
				/>

				<SubHeading id="v3-rec-timing">Choose the alert period and run mode</SubHeading>
				<P>
					These controls are intentionally separate. The <strong>alert grouping period</strong>{' '}defines the
					lifecycle of an alert thread; <strong>run mode</strong>{' '}defines when a new observation is made.
				</P>
				<GuideTable
					caption="Alert grouping and evaluation timing"
					columns={['Setting', 'Practical effect']}
					rows={[
						[<strong key="continuous">Continuous</strong>, 'Matching failed observations keep returning to one ongoing alert thread; there is no period rollover'],
						[<strong key="periodic">Daily / weekly / monthly</strong>, 'Within the active period, matching observations share one alert that can be resolved and reopened. After rollover, the next matching failure creates a new alert'],
						[<strong key="on-demand">On demand</strong>, 'The rule evaluates only when someone selects Run now'],
						[<strong key="automatic">Automatic schedule</strong>, 'A preset or custom cron expression triggers evaluations in the configured time zone'],
					]}
				/>
				<Callout kind="info" title="Period does not mean evaluation frequency">
					A daily alert period does not necessarily mean one evaluation per day. You can evaluate many
					times during that day; matching failures become observations on the same daily alert thread.
				</Callout>

				<SubHeading id="v3-rec-run">Running and toggling</SubHeading>
				<P>
					<strong>Run now</strong>{' '}in the list—or <strong>Evaluate now</strong>{' '}in rule details—runs a rule immediately and reports <em>Pass</em>, <em>Fail</em>, or{' '}
					<em>Error</em>. A pass or fail is recorded as a <AppLink href="/guide?section=activity">capture</AppLink>; a fail
					also opens or updates an <AppLink href="/guide?section=alerts">alert</AppLink>. The <strong>On / Off</strong>{' '}
					switch turns monitoring on or off without deleting the rule. Run now stays in place while the
					switch updates, and is unavailable while the rule is off. Results settle a moment after an
					evaluation, so views refresh themselves briefly rather than assuming an instant update.
				</P>
				<P>
					A failed latest-result badge is a shortcut to the alert raised by that run. Select the rule name
					or the rest of its header to open rule details instead.
				</P>

				<SubHeading id="v3-rec-manage">Editing, duplicating & finding rules</SubHeading>
				<ul className="list-disc space-y-1 pl-5 text-sm">
					<li>Open a rule, then use <strong>Edit</strong>{' '}to change its name, configuration, or severity. The compiled CEL is regenerated from the human-readable form, and the alert period is fixed after creation.</li>
					<li><strong>Duplicate</strong>{' '}is beside Edit in rule details and starts a new rule prefilled from the existing one.</li>
					<li>Use search, template, severity, monitoring-status, and sort controls to narrow the list. Pagination appears when more than one page of matching rules exists.</li>
				</ul>

				<Callout kind="tip" title="Invalid configuration">
					If a rule&apos;s configuration is rejected (an unsupported operator, or a metadata key that
					isn&apos;t indexed on the ledger), the service names the offending field and the form shows the
					message inline. Fix it and try again.
				</Callout>
			</Section>
		),
	},
	{
		id: 'activity',
		group: 'Reconciliation',
		label: 'Rule activity',
		icon: <History className="size-4" aria-hidden />,
		render: () => (
			<Section
				id="activity"
				eyebrow="Reconciliation"
				title="Rule activity"
				intro="Open a rule to see its current applied configuration followed by one durable activity timeline: configuration revisions, evaluation receipts, and the alert lifecycle they caused."
			>
				<SubHeading id="v3-rec-rule-detail">Use rule details as the control record</SubHeading>
				<P>
					Select a rule&apos;s header to open it. The top of the page restates the configured control in the
					same human-readable format used in the Rules list: the named sources, and either{' '}
					<strong>= Exact match</strong>{' '}or <strong>≈ Within tolerance</strong>. Edit, Duplicate, and
					Evaluate live together in the detail header. Sources are shown before the invariant they feed, and
					generated CEL is collapsed under <strong>Implementation details</strong>.
				</P>
				<P>
					One <strong>Combined history</strong>{' '}then follows in the exact order recorded by the backend
					journal. Captures are the evaluation receipts within that wider control record; the page does not
					reconstruct history from current alerts or older capture lists.
				</P>

				<SubHeading id="v3-rec-timeline">Reading the timeline</SubHeading>
				<P>
					Activity is listed newest first. Each evaluation shows its result (<em>pass</em>, <em>fail</em>, or
					<em> error</em>), whether it was triggered manually or on schedule, its period, duration, and the
					rule revision it used. A failing evaluation receipt carries the{' '}
					<strong>evidence</strong>{' '}behind the verdict: for a parity check, the balance on each side, their
					difference, and the tolerance; for a threshold, the balance and the bound it crossed.
				</P>
				<P>
					Alert consequences carrying the same <strong>evaluation id</strong> are grouped with that evaluation
					without changing their journal sequence. Manual alert actions have no evaluation correlation and
					remain standalone entries, making the distinction between automated consequences and operator work clear.
				</P>

				<SubHeading id="v3-rec-captures-vs">Revisions and history coverage</SubHeading>
				<P>
					Every saved configuration has a revision. Past evaluations stay tied to the revision actually used,
					and a warning appears when it differs from the current revision. Creation, update, and deletion
					entries can expose their complete snapshots; field differences are shown only when both adjacent
					snapshots have been loaded.
				</P>

				<Callout kind="info" title="The journal is forward-only">
					Activity recorded before the journal was introduced is not inferred from current state. When earlier
					lifecycle events were never recorded, the timeline names the date from which durable activity is available.
					A passing evaluation receipt remains valuable evidence that the control held.
				</Callout>
			</Section>
		),
	},
	{
		id: 'alerts',
		group: 'Reconciliation',
		label: 'Alerts & resolution',
		icon: <Bell className="size-4" aria-hidden />,
		render: () => (
			<Section
				id="alerts"
				eyebrow="Reconciliation"
				title="Alerts & resolution"
				intro="When a rule fails it opens an alert, a break to work. The Alerts tab is the inbox: triage by status and severity, read the evidence, and record how each break was handled."
			>
				<SubHeading id="v3-rec-inbox">The inbox</SubHeading>
				<P>
					The inbox opens on <strong>All</strong>{' '}alerts and can be narrowed to <strong>Open</strong>,{' '}
					<strong>Acknowledged</strong>, or <strong>Resolved</strong>; each status filter includes a count.
					The Overview shows only recent open alerts, while its <strong>All alerts</strong>{' '}link returns here
					without narrowing the inbox. The catalogue filters follow the Rules list: search by fingerprint or
					rule name, narrow by rule, severity, or status, then sort by latest, oldest, severity, or observation
					count. Selection, grouping, refresh, and bulk actions sit on a separate operations row so they are
					not confused with filters. Each compact card has a header for severity, status, rule, and recurrence,
					followed by a human-readable comparison from the latest observation. Select the fingerprint
					or open header space to view alert details; select the <strong>rule name</strong>{' '}to open that
					rule&apos;s <AppLink href="/guide?section=activity">combined activity timeline</AppLink>.
				</P>
				<ul className="list-disc space-y-1 pl-5 text-sm">
					<li><strong>Selection-based triage:</strong>{' '}tick one or more actionable cards and the labelled acknowledge, resolve, accept, and snooze controls appear in the operations row. This is also the bulk workflow.</li>
					<li><strong>Resolved alerts:</strong>{' '}remain available for inspection but cannot be selected because there is no pending triage action to apply.</li>
					<li><strong>Group by rule:</strong>{' '}creates a collapsible section for each rule on the current page. Its own checkbox selects only that rule&apos;s visible alerts, which is useful when ownership follows the control.</li>
					<li><strong>Auto-refresh:</strong>{' '}polls every ten seconds for new and changed breaks. Leave it off when you need a stable selection for careful bulk work.</li>
				</ul>

				<SubHeading id="v3-rec-meta-alerts">Alerts about the check, not the money</SubHeading>
				<P>
					Two entries can appear in the inbox that describe the module&apos;s own behaviour rather than a
					financial break. Both carry a <Code>kind</Code>{' '}label, so notifications can route them
					somewhere other than the channel your reconciliation breaks go to.
				</P>
				<GuideTable
					caption="Alerts about the check"
					columns={['Alert', 'What it means', 'What to do']}
					rows={[
						[<strong key="err">engine.error</strong>, 'The check could not run — a resolver timed out, a query failed, a budget ran out. It says nothing about the money.', 'Look at the rule’s configuration and the ledger connection. It clears itself once an evaluation completes.'],
						[<strong key="cap">alert.cap</strong>, 'The check ran and found more new breaks than the module opens at once, so it opened none of them and raised this instead. The evaluation and its full evidence are still recorded.', 'Read the count and the sample it carries: this is one systemic failure, not hundreds of separate ones. Fix the cause or narrow the rule, and the next run alerts normally.'],
					]}
				/>
				<Callout kind="info" title="Why none were opened rather than the first few">
					An alert resolves itself once its rule stops reporting it. Opening only part of an oversized
					batch would make the rest look resolved — closing breaks precisely because there were too many
					of them to report. Withholding the whole batch leaves every alert already open exactly as it
					was, and nothing is lost from the record: the run and its evidence are still captured.
				</Callout>

				<SubHeading id="v3-rec-occurrences">Occurrences and evidence</SubHeading>
				<P>
					Within the active alert period, another failed observation with the same fingerprint returns to
					the same alert: its <strong>occurrence count</strong>{' '}increases and the latest evidence is updated.
					The alert may be resolved and later reopened while that period is active. When a daily, weekly,
					or monthly period rolls over, the next matching failure starts a new alert thread. Continuous
					rules have no rollover and keep one ongoing thread.
				</P>

				<SubHeading id="v3-rec-what-drove">What drove the break</SubHeading>
				<P>
					Alert details use one unified <strong>Evaluation history</strong>. The latest evaluation is
					highlighted first with its verdict, trigger, time, evaluation id, and evidence. Earlier captures
					whose evidence includes the same fingerprint are collapsed underneath; expand one to compare how
					the numbers changed. The header also links back to the rule and shows first seen, last seen, and
					the occurrence count.
				</P>
				<Callout kind="info" title="Captures are evaluation receipts">
					The rule&apos;s <AppLink href="/guide?section=activity">combined activity timeline</AppLink> is the canonical control
					record. Capture evidence remains the receipt for an individual evaluation, while the backend journal
					records configuration and alert lifecycle activity around it.
				</Callout>

				<SubHeading id="v3-rec-actions">Working a break</SubHeading>
				<GuideTable
					caption="Resolution actions"
					columns={['Action', 'Use it when', 'Effect']}
					rows={[
						[<strong key="acknowledge">Acknowledge</strong>, 'You are investigating', 'Marks the break as being worked; notifications keep flowing'],
						[<strong key="resolve">Resolve</strong>, 'The break is fixed', 'Closes it; adding booking transaction references records it as fixed by booking'],
						[<strong key="accept">Accept</strong>, 'It is a known, tolerated difference', 'Closes it as accepted by the business (a note is required)'],
						[<strong key="snooze">Snooze / Unsnooze</strong>, 'You are waiting on something (e.g. a bank file)', 'Mutes notifications until a future time you set, then lifts the mute'],
					]}
				/>
				<P>
					You can act from alert details or select one or more alerts in the inbox to reveal the same labelled
					actions. The action form shows how many alerts will be changed. Every action records the
					actor and an optional note; acceptance requires a reason, resolve can include comma-separated
					booking transaction references, and snooze requires a future time. Alert details show these
					decisions together under <strong>Lifecycle</strong>.
				</P>

				<Callout kind="tip" title="Resolve vs. accept">
					<strong>Resolve</strong>{' '}says &quot;the books were corrected&quot; (ideally with the booking
					reference that fixed them). <strong>Accept</strong>{' '}says &quot;this difference is expected and we
					are choosing to live with it.&quot; Both close the break, but they tell very different stories
					later.
				</Callout>
			</Section>
		),
	},
	{
		id: 'insights',
		group: 'Reconciliation',
		label: 'Insights',
		icon: <LineChart className="size-4" aria-hidden />,
		render: () => (
			<Section
				id="insights"
				eyebrow="Reconciliation"
				title="Reconciliation insights"
				intro="Insights turns capture and alert history into operational signals. Use it after rules have run several times: the charts help separate isolated breaks from persistent bias and show where the alert workload is concentrated."
			>
				<SubHeading id="v3-rec-insights-start">Start with a rule and asset</SubHeading>
				<ol className="list-decimal space-y-1 pl-7 text-sm">
					<li>Choose a rule from the selector. Balance-equation and balance-bounds rules provide deviation charts.</li>
					<li>If the rule covers several assets or fingerprints, choose the series you want to investigate.</li>
					<li>Hover a point for its exact time, verdict, observed value, and configured boundary.</li>
					<li>Return to the rule&apos;s combined activity timeline when you need the complete evidence for a specific run.</li>
				</ol>

				<SubHeading id="v3-rec-insights-charts">Read the charts</SubHeading>
				<GuideTable
					caption="Insights charts"
					columns={['Chart', 'How to use it']}
					rows={[
						[<strong key="deviation">Deviation over time</strong>, 'Drawn for the two templates that measure a distance from a reference: a balance equation plots its residual against the ± tolerance band, and balance bounds plots the balance against its minimum and maximum. Other templates have no single deviation to plot, so the chart is omitted rather than faked'],
						[<strong key="passes">Pass markers</strong>, 'Show that a run stayed safe. A pass capture does not record a magnitude, so the marker is placed inside the safe zone without inventing a value'],
						[<strong key="drift">Cumulative drift</strong>, 'Adds the signed gap over time. A line that keeps rising or falling indicates persistent one-sided bias; movement that returns toward zero behaves more like offsetting noise'],
						[<strong key="breaks">Breaks by rule type</strong>, 'Shows the global alert count split by template and status, helping identify which class of control creates the most operational work'],
					]}
				/>

				<Callout kind="info" title="What Insights does not invent">
					Failed captures contain the measured deviation and can be plotted precisely. Passing captures
					prove that the control held, but may not store the exact safe value; the chart therefore shows a
					pass marker rather than fabricating a measurement.
				</Callout>

				<Callout kind="tip" title="No chart yet?">
					Evaluate the rule from its detail screen to create an evaluation receipt in rule activity. Ledger-invariant rules still
					contribute to break distribution, but their normalized sum is not currently plotted in the
					deviation chart.
				</Callout>
			</Section>
		),
	},
	{
		id: 'audit',
		group: 'Reconciliation',
		label: 'Audit & verification',
		icon: <ShieldCheck className="size-4" aria-hidden />,
		render: () => (
			<Section
				id="audit"
				eyebrow="Reconciliation"
				title="Audit & verification"
				intro="Every action Reconcile records — each evaluation, each break, each resolution — is cryptographically signed on its control ledger. The Audit tab turns that into something a third party can check for themselves: the record is verifiable without trusting Formance, the ledger operator, or this screen."
			>
				<SubHeading id="v3-rec-audit-what">What is signed, and why it matters</SubHeading>
				<P>
					Reconcile writes its state to a dedicated control ledger and signs every write with an Ed25519 key
					it holds privately. The signature travels with the entry. Anyone holding the matching{' '}
					<em>public</em> key can confirm two things about any entry: that Reconcile produced it
					(authorship), and that nothing has altered it since (integrity). A missing, reordered, or edited
					record fails the check — so the trail cannot be quietly rewritten after the fact.
				</P>

				<SubHeading id="v3-rec-audit-panel">What the Audit tab shows</SubHeading>
				<GuideTable
					caption="The Audit tab"
					columns={['Panel', 'What it gives you']}
					rows={[
						[<strong key="verify">Verification</strong>, 'The public signing key (copyable) and a short recipe for checking any entry yourself'],
						[<strong key="activity">Signed activity</strong>, 'The breaks that have been handled, each showing who acted and how trustworthy that identity is'],
					]}
				/>

				<SubHeading id="v3-rec-audit-actor">Who did what: verified vs declared</SubHeading>
				<P>
					Each handling action carries a provenance badge. <strong>Verified</strong> means the actor is the
					subject of an authenticated access token, bound inside the signature — the record proves the named
					person acted. <strong>Declared</strong> means only a self-typed name was supplied (for example,
					when Reconcile runs without authentication in a local environment); it is kept for display but is
					not a trust anchor.
				</P>

				<SubHeading id="v3-rec-audit-auditor">For an external auditor</SubHeading>
				<P>
					The point of signing is that your auditor does not have to take our word for anything. Hand them the
					public key from the verification panel and they can check the reconciliation record end to end, with
					no access to our systems and no involvement from us:
				</P>
				<ol className="list-decimal space-y-1 pl-7 text-sm">
					<li>Copy the <strong>public key</strong> from the verification panel.</li>
					<li>Read a control-ledger entry — its signed payload (the exact bytes of the batch that was committed) and its signature.</li>
					<li>Run <Code>ed25519.Verify(publicKey, payload, signature)</Code> in any language. It passes only if Reconcile wrote that exact entry and nothing changed it afterwards.</li>
					<li>Repeat for any entries you like — each one verifies on its own, proving authorship and integrity from the public key alone.</li>
				</ol>

				<Callout kind="tip" title="Verify a single action from the timeline">
					You don&apos;t have to start from the entry list. On an alert&apos;s timeline, expand any event —
					opened, acknowledged, resolved — to resolve it to its signed audit entry and verify the signature
					in-browser, right there. It is the same check as the recipe above, scoped to one action.
				</Callout>

				<Callout kind="info" title="Why this beats a report">
					A dashboard that says &quot;all reconciled&quot; asks you to trust the dashboard. A signed ledger
					hands the auditor the evidence <em>and</em> the means to check it independently: the public key is
					the whole trust anchor, and Formance is never in the loop. That is the difference between being told
					the books reconcile and being able to prove it.
				</Callout>

				<Callout kind="info" title="What signing proves — and what it doesn’t">
					Verifying an entry proves <strong>authorship and integrity</strong>: Reconcile wrote it and nothing
					changed it, provable from the public key alone. <strong>Completeness</strong> — that no action went
					unrecorded — is a separate guarantee. The entry sequence is shared across every ledger in the bucket,
					so gaps in the reconciliation entries are expected and a &quot;no-gaps&quot; check on them proves
					nothing; completeness is anchored in the ledger, not the public key.
				</Callout>
			</Section>
		),
	},
]
