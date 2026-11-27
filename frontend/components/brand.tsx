import { FormanceLogo } from '@workspace/ui/components/formance-logo'
import { BadgeEyebrow } from '@workspace/ui/components/badge-eyebrow'
import { cn } from '@/lib/utils'

/** Formance wordmark + the module eyebrow badge — the console house branding. */
export function Brand({ className, stacked = false }: { className?: string; stacked?: boolean }) {
	// Stacked in the 240 px sidebar: logo and badge side by side are wider than
	// it, and the badge used to spill over the tab bar next to it.
	return (
		<div className={cn(stacked ? 'flex min-w-0 flex-col items-start gap-1' : 'flex min-w-0 items-center gap-2.5', className)}>
			<FormanceLogo className={cn('shrink-0 text-foreground', stacked ? 'w-24' : 'w-28')} />
			<BadgeEyebrow variant="emerald">Reconciliation</BadgeEyebrow>
		</div>
	)
}
