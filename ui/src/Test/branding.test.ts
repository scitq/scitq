// The sidebar and login page show the installation's branding, which the
// server writes into index.html as window.SCITQ_BRANDING.
import { render, screen } from '@testing-library/svelte';
import { describe, it, expect, afterEach } from 'vitest';
import { tick } from 'svelte';
import SideBar from '../components/SideBar.svelte';
import LoginPage from '../pages/LoginPage.svelte';
import { theme } from '../lib/Stores/theme';

function renderWith(branding: object | undefined, page: 'SideBar' | 'LoginPage') {
  (window as any).SCITQ_BRANDING = branding;
  render(page === 'SideBar' ? SideBar : LoginPage);
}

afterEach(() => {
  delete (window as any).SCITQ_BRANDING;
  theme.set('light');
});

describe('branding', () => {
  for (const page of ['SideBar', 'LoginPage'] as const) {
    it(`${page} shows the configured name and logo`, async () => {
      renderWith({ name: 'GMT SciTQ', logo: '/branding/logo' }, page);
      expect(screen.getByTestId('brand-name').textContent).toBe('GMT SciTQ');
      expect(screen.getByTestId('brand-logo').getAttribute('src')).toBe('/branding/logo');
    });

    it(`${page} shows the logo for dark backgrounds, in both themes`, async () => {
      renderWith({ logo: '/branding/logo', logoDark: '/branding/logo-dark' }, page);
      const logo = screen.getByTestId('brand-logo');
      expect(logo.getAttribute('src')).toBe('/branding/logo-dark');
      theme.set('dark');
      await tick();
      expect(logo.getAttribute('src')).toBe('/branding/logo-dark');
    });

    if (page === 'SideBar') {
      it('SideBar shows the logo that suits its background in each theme', async () => {
        renderWith({ logo: '/branding/logo', logoDark: '/branding/logo-dark', lightSidebar: ['light'] }, page);
        const logo = screen.getByTestId('brand-logo');
        expect(logo.getAttribute('src')).toBe('/branding/logo');
        theme.set('dark');
        await tick();
        expect(logo.getAttribute('src')).toBe('/branding/logo-dark');
      });
    }

    it(`${page} shows scitq's own branding when none is configured`, async () => {
      renderWith(undefined, page);
      expect(screen.getByTestId('brand-name').textContent).toBe('SciTQ');
      expect(screen.getByTestId('brand-logo').getAttribute('src')).toContain('logoSCITQ2');
    });
  }
});
