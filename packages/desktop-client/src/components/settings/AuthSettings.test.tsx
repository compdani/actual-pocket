import { render, screen } from '@testing-library/react';
import { vi } from 'vitest';

import { useServerURL } from '#components/ServerContext';
import { useSyncServerStatus } from '#hooks/useSyncServerStatus';
import { TestProviders } from '#mocks';

import { AuthSettings } from './AuthSettings';

vi.mock('#hooks/useSyncServerStatus', () => ({
  useSyncServerStatus: vi.fn(),
}));
vi.mock('#components/ServerContext', () => ({
  useServerURL: vi.fn(),
}));

describe('AuthSettings', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(useServerURL).mockReturnValue('https://actual.example.com');
  });

  it('does not render when server status is no-server', () => {
    vi.mocked(useSyncServerStatus).mockReturnValue('no-server');

    const { container } = render(<AuthSettings />, { wrapper: TestProviders });

    expect(container.firstChild).toBeNull();
  });

  it('links to the PocketBase admin users collection', () => {
    vi.mocked(useSyncServerStatus).mockReturnValue('online');

    render(<AuthSettings />, { wrapper: TestProviders });

    const link = screen.getByRole('link', { name: /open the admin/i });
    expect(link).toHaveAttribute(
      'href',
      'https://actual.example.com/_/#/collections?collection=users',
    );
  });

  it('does not render the link while the server is offline', () => {
    vi.mocked(useSyncServerStatus).mockReturnValue('offline');

    render(<AuthSettings />, { wrapper: TestProviders });

    expect(
      screen.queryByRole('link', { name: /open the admin/i }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText(/user and sign-in settings are unavailable/i),
    ).toBeInTheDocument();
  });

  it('does not render the link without a server URL', () => {
    vi.mocked(useSyncServerStatus).mockReturnValue('online');
    vi.mocked(useServerURL).mockReturnValue(null);

    render(<AuthSettings />, { wrapper: TestProviders });

    expect(
      screen.queryByRole('link', { name: /open the admin/i }),
    ).not.toBeInTheDocument();
  });
});
