// The installation's branding (ui: in scitq.yaml). The server writes it
// into index.html as window.SCITQ_BRANDING, so it is known before the first
// render; without it (the Vite dev server) scitq's own look applies.
import defaultLogo from '../assets/icons/logoSCITQ2.png';

export function branding(): { name: string; logo: string } {
  const injected = (typeof window !== 'undefined' && (window as any).SCITQ_BRANDING) || {};
  return { name: injected.name || 'SciTQ', logo: injected.logo || defaultLogo };
}
