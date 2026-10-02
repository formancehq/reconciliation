import { FormanceLogo } from '@workspace/ui/components/formance-logo'
import { BadgeEyebrow } from '@workspace/ui/components/badge-eyebrow'
import { cn } from '@/lib/utils'

/**
 * Formance wordmark + the module eyebrow badge — the console house branding.
 * `compact` drops the eyebrow on a phone, where the embedding console already
 * names the module.
 */
export function Brand({ className, compact }: { className?: string; compact?: boolean }) {
	return (
		<div className={cn('flex shrink-0 items-center gap-2.5', className)}>
			<FormanceLogo className="w-28 text-foreground" />
			<BadgeEyebrow variant="emerald" className={compact ? 'hidden sm:inline-flex' : undefined}>
				Reconciliation
			</BadgeEyebrow>
		</div>
	)
}
