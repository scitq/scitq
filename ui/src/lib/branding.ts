// The installation's branding (ui: in scitq.yaml). The server writes it
// into index.html as window.SCITQ_BRANDING, so it is known before the first
// render; without it (the Vite dev server) scitq's own look applies.
import defaultLogo from '../assets/icons/logoSCITQ2.png';

// logoDark is the logo for dark backgrounds; it is logo when none is set.
// lightSidebar lists the themes ('light', 'dark') whose sidebar is light.
export function branding(): { name: string; logo: string; logoDark: string; lightSidebar: string[] } {
  const injected = (typeof window !== 'undefined' && (window as any).SCITQ_BRANDING) || {};
  const logo = injected.logo || defaultLogo;
  return {
    name: injected.name || 'SciTQ',
    logo,
    logoDark: injected.logoDark || logo,
    lightSidebar: injected.lightSidebar || [],
  };
}
