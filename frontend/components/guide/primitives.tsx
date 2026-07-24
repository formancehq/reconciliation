'use client'

// Presentation primitives for the business User Guide, styled with the Formance
// design-system tokens (sharp corners, oklch color families). Mirrors the console
// shell's guide primitives, adapted to this app.

import * as React from 'react'
import Link from 'next/link'
import { AlertTriangle, Check, Info, Lightbulb } from 'lucide-react'
import { cn } from '@/lib/utils'

export const prefersReducedMotion = () =>
	typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches

export function scrollToEl(el: Element | null) {
	if (!el) return
	el.scrollIntoView({ behavior: prefersReducedMotion() ? 'auto' : 'smooth', block: 'start' })
}

/** A guide section. `title` becomes an <h2>; use <SubHeading> for TOC entries. */
export function Section({
	id,
	eyebrow,
	title,
	intro,
	children,
}: {
	id: string
	eyebrow?: string
	title: string
	intro?: React.ReactNode
	children: React.ReactNode
}) {
	return (
		<section id={`guide-${id}`} aria-labelledby={`h-${id}`} className="max-w-3xl space-y-5">
			<header className="space-y-2">
				{eyebrow ? (
					<div className="font-mono text-xs uppercase tracking-wider text-muted-foreground">{eyebrow}</div>
				) : null}
				<h2 id={`h-${id}`} className="font-heading text-2xl font-semibold tracking-tight">
					{title}
				</h2>
				{intro ? <p className="text-[15px] leading-relaxed text-muted-foreground">{intro}</p> : null}
			</header>
			<div className="space-y-4 text-[15px] leading-relaxed text-foreground/90">{children}</div>
		</section>
	)
}

/** A subsection heading that the right-rail "On this page" TOC picks up (h3[id]). */
export function SubHeading({ id, children }: { id: string; children: React.ReactNode }) {
	return (
		<h3 id={id} className="scroll-mt-6 pt-2 font-heading text-base font-semibold tracking-tight">
			{children}
		</h3>
	)
}

export function P({ children }: { children: React.ReactNode }) {
	return <p className="leading-relaxed">{children}</p>
}

export function Lead({ children }: { children: React.ReactNode }) {
	return <p className="text-base leading-relaxed text-muted-foreground">{children}</p>
}

/** Emphasis for a domain term inline. */
export function Term({ children }: { children: React.ReactNode }) {
	return <strong className="font-semibold text-foreground">{children}</strong>
}

/** Inline monospace code / identifier. */
export function Code({ children }: { children: React.ReactNode }) {
	return <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">{children}</code>
}

type CalloutKind = 'info' | 'success' | 'warning' | 'tip'

const CALLOUT: Record<CalloutKind, { box: string; icon: React.ComponentType<{ className?: string }>; accent: string }> = {
	info: { box: 'bg-blue-background/50 border-blue-foreground/25', icon: Info, accent: 'text-blue-foreground' },
	success: { box: 'bg-green-background/50 border-green-foreground/25', icon: Check, accent: 'text-green-foreground' },
	warning: { box: 'bg-amber-background/60 border-amber-foreground/25', icon: AlertTriangle, accent: 'text-amber-foreground' },
	tip: { box: 'bg-emerald-100/60 border-emerald-600/25', icon: Lightbulb, accent: 'text-emerald-700' },
}

export function Callout({ kind = 'info', title, children }: { kind?: CalloutKind; title?: string; children: React.ReactNode }) {
	const c = CALLOUT[kind]
	const Icon = c.icon
	return (
		<div role={kind === 'warning' ? 'alert' : 'note'} className={cn('flex items-start gap-2.5 border p-3.5 text-sm', c.box)}>
			<Icon className={cn('mt-0.5 size-4 shrink-0', c.accent)} aria-hidden />
			<div className="space-y-1">
				{title ? <p className={cn('font-semibold', c.accent)}>{title}</p> : null}
				<div className="space-y-2 text-foreground/90">{children}</div>
			</div>
		</div>
	)
}

/** A compact reference table. */
export function GuideTable({ columns, rows, caption }: { columns: string[]; rows: React.ReactNode[][]; caption?: string }) {
	return (
		<div className="overflow-x-auto border">
			<table className="w-full border-collapse text-sm">
				{caption ? <caption className="sr-only">{caption}</caption> : null}
				<thead>
					<tr className="border-b bg-muted/50">
						{columns.map((c, i) => (
							<th key={i} scope="col" className="p-2.5 text-left font-semibold">
								{c}
							</th>
						))}
					</tr>
				</thead>
				<tbody>
					{rows.map((r, i) => (
						<tr key={i} className="border-b last:border-b-0">
							{r.map((cell, j) => (
								<td key={j} className="p-2.5 align-top">
									{cell}
								</td>
							))}
						</tr>
					))}
				</tbody>
			</table>
		</div>
	)
}

/** A link to a real page in the app (opens in the same frame). */
export function AppLink({ href, children }: { href: string; children: React.ReactNode }) {
	return (
		<Link href={href} className="font-medium text-primary underline underline-offset-4 hover:opacity-80">
			{children}
		</Link>
	)
}

/** An ordered set of steps (for the walkthrough). */
export function Steps({ items }: { items: { title: React.ReactNode; body: React.ReactNode }[] }) {
	return (
		<ol className="space-y-4">
			{items.map((s, i) => (
				<li key={i} className="flex gap-3">
					<span className="flex size-6 shrink-0 items-center justify-center bg-primary text-xs font-semibold text-primary-foreground">
						{i + 1}
					</span>
					<div className="space-y-1 pt-0.5">
						<div className="font-medium">{s.title}</div>
						<div className="text-sm text-muted-foreground">{s.body}</div>
					</div>
				</li>
			))}
		</ol>
	)
}
