'use client';

/**
 * Switch which recon backend the Reconcile UI talks to.
 *
 * The recon server binds to one ledger at startup (`--ledger-address`), so to
 * reconcile ledgers on another server (e.g. staging) you point the UI at a
 * recon instance wired to that server. This sets the per-request `X-Recon-Url`
 * (+ optional bearer token) the proxy forwards; empty = the server default.
 */
import { useState } from 'react';
import { Server, Check } from 'lucide-react';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { reconEndpointLabel } from '@/lib/recon';
import { useReconNav } from './ReconContext';

export function EndpointSwitcher() {
  const { endpoint, setEndpoint } = useReconNav();
  const [open, setOpen] = useState(false);
  const [url, setUrl] = useState(endpoint.url);
  const [token, setToken] = useState(endpoint.token ?? '');

  const onOpenChange = (o: boolean) => {
    if (o) { setUrl(endpoint.url); setToken(endpoint.token ?? ''); }
    setOpen(o);
  };
  const apply = () => { setEndpoint({ url: url.trim(), token: token.trim() || undefined }); setOpen(false); };
  const useDefault = () => { setEndpoint({ url: '', token: '' }); setUrl(''); setToken(''); setOpen(false); };

  const custom = !!endpoint.url;

  return (
    <Popover open={open} onOpenChange={onOpenChange}>
      <PopoverTrigger asChild>
        <button
          type="button"
          title="Reconciliation backend — click to switch"
          className="flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs text-muted-foreground transition-colors hover:bg-accent"
        >
          <Server className="h-3 w-3 shrink-0" />
          <span className="max-w-[11rem] truncate">{reconEndpointLabel(endpoint)}</span>
        </button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80 space-y-3">
        <div>
          <p className="text-sm font-semibold">Reconciliation backend</p>
          <p className="text-xs text-muted-foreground">
            Point the UI at a different recon instance (e.g. one wired to a staging ledger). Leave blank to use the server default.
          </p>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="recon-switcher-url" className="text-xs">Base URL</Label>
          <Input id="recon-switcher-url" value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://reconciliation.<stack>.frmnc.net" />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="recon-switcher-token" className="text-xs">Bearer token <span className="text-muted-foreground">(optional — for authenticated instances)</span></Label>
          <Input id="recon-switcher-token" type="password" value={token} onChange={(e) => setToken(e.target.value)} placeholder="eyJ…" autoComplete="off" />
          <p className="text-[11px] text-muted-foreground">Stored for this tab until it is closed; sent as Authorization to the proxy.</p>
        </div>
        <div className="flex items-center justify-between">
          <Button variant="ghost" size="sm" onClick={useDefault} disabled={!custom && !token}>Use default</Button>
          <Button size="sm" onClick={apply}><Check className="mr-1.5 h-3.5 w-3.5" /> Apply</Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}
