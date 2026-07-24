import { FormanceLogo } from '@workspace/ui/components/formance-logo'
import { BadgeEyebrow } from '@workspace/ui/components/badge-eyebrow'
import { cn } from '@/lib/utils'

/** Formance wordmark + the module eyebrow badge — the console house branding. */
export function Brand({ className }: { className?: string }) {
	return (
		<div className={cn('flex items-center gap-2.5', className)}>
			<FormanceLogo className="w-28 text-foreground" />
			<BadgeEyebrow variant="emerald">Reconciliation</BadgeEyebrow>
		</div>
	)
}
