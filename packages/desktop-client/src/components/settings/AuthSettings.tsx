import React from 'react';
import { Trans, useTranslation } from 'react-i18next';

import { Text } from '@actual-app/components/text';
import { theme } from '@actual-app/components/theme';

import { Link } from '#components/common/Link';
import { useServerURL } from '#components/ServerContext';
import { useSyncServerStatus } from '#hooks/useSyncServerStatus';

import { SettingsRow } from './SettingsRow';

export function AuthSettings() {
  const { t } = useTranslation();

  const serverURL = useServerURL();
  const serverStatus = useSyncServerStatus();

  // Hide the block entirely when no server is configured
  if (serverStatus === 'no-server') {
    return null;
  }

  const isOffline = serverStatus === 'offline';
  const usersURL = serverURL
    ? `${serverURL}/_/#/collections?collection=users`
    : null;

  return (
    <SettingsRow
      title={t('Users and sign-in methods')}
      description={t(
        'Users, roles, passwords, and OAuth2 providers are managed in the PocketBase admin.',
      )}
      control={
        usersURL && !isOffline ? (
          <Link variant="external" to={usersURL}>
            <Trans>Open the admin</Trans>
          </Link>
        ) : undefined
      }
    >
      {isOffline && (
        <Text style={{ color: theme.warningText }}>
          <Trans>
            Server is offline. User and sign-in settings are unavailable.
          </Trans>
        </Text>
      )}
    </SettingsRow>
  );
}
