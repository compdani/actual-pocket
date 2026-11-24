import React, { useEffect, useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';

import { Button, ButtonWithLoading } from '@actual-app/components/button';
import { BigInput } from '@actual-app/components/input';
import { Label } from '@actual-app/components/label';
import { Text } from '@actual-app/components/text';
import { theme } from '@actual-app/components/theme';
import { View } from '@actual-app/components/view';
import type {
  DesktopHost,
  DesktopSyncServerStatus,
} from '@actual-app/core/typings/window';
import { css } from '@emotion/css';

import { Link } from '#components/common/Link';
import { useSetServerURL } from '#components/ServerContext';
import { useNavigate } from '#hooks/useNavigate';

import { Title } from './subscribe/common';

const numberInputClassName = css({
  '&::-webkit-outer-spin-button, &::-webkit-inner-spin-button': {
    WebkitAppearance: 'none',
    margin: 0,
  },
});

type LocalServerConfigProps = {
  desktop: DesktopHost;
  status: DesktopSyncServerStatus;
  onDoNotUseServer: () => void;
  onUseExternalServer?: () => void;
};

// The desktop app's built-in sync server: it runs on this computer, on the
// chosen port, and starts with the app until it is stopped
export function LocalServerConfig({
  desktop,
  status,
  onDoNotUseServer,
  onUseExternalServer,
}: LocalServerConfigProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const setServerUrl = useSetServerURL();

  const [port, setPort] = useState(status.port);
  const [isRunning, setIsRunning] = useState(status.running);
  const [isStarting, setIsStarting] = useState(false);
  const [configError, setConfigError] = useState<string | null>(null);

  useEffect(() => {
    let isCancelled = false;
    void desktop.getSyncServerStatus().then(next => {
      if (!isCancelled) {
        setIsRunning(next.running);
      }
    });
    return () => {
      isCancelled = true;
    };
  }, [desktop]);

  async function onStart() {
    if (isStarting) {
      return;
    }
    if (!Number.isInteger(port) || port <= 0 || port > 65535) {
      setConfigError(t('Ports must be within range 1 - 65535'));
      return;
    }
    setConfigError(null);
    setIsStarting(true);
    try {
      await desktop.startSyncServer(port);
      setIsRunning(true);
      await setServerUrl(`http://localhost:${port}`);
      void navigate('/');
    } catch (error) {
      console.error('Failed to configure sync server:', error);
      setConfigError(
        t('Failed to start the server: {{error}}', {
          error: error instanceof Error ? error.message : String(error),
        }),
      );
    } finally {
      setIsStarting(false);
    }
  }

  async function onDontUseServer() {
    if (isRunning) {
      await desktop.stopSyncServer();
    }
    onDoNotUseServer();
  }

  return (
    <>
      <Title text={t('Configure your server')} />
      <View style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
        <Text style={{ fontSize: 16, color: theme.pageText, lineHeight: 1.5 }}>
          <Trans>
            Set up your server below to enable seamless data synchronization
            across your devices, bank sync and more...
          </Trans>
        </Text>
        <Text style={{ fontSize: 16, color: theme.pageText, lineHeight: 1.5 }}>
          <Trans>
            Need to expose your server to the internet? Follow our step-by-step{' '}
            <Link
              variant="external"
              to="https://actualbudget.org/docs/install/desktop-app"
            >
              guide
            </Link>{' '}
            for more information.
          </Trans>
        </Text>

        {configError && (
          <Text style={{ color: theme.errorText, marginTop: 10 }}>
            {configError}
          </Text>
        )}

        <View style={{ display: 'flex', flexDirection: 'row', gap: 10 }}>
          <View style={{ flexDirection: 'column', gap: 5, flex: 1 }}>
            <Label title={t('Domain')} style={{ textAlign: 'left' }} />
            <BigInput
              value="localhost"
              disabled
              type="text"
              className={numberInputClassName}
            />
          </View>

          <View style={{ flexDirection: 'column', gap: 5 }}>
            <Label
              title={t('Port')}
              style={{ textAlign: 'left', width: '7ch' }}
            />
            <BigInput
              name="port"
              value={String(port)}
              aria-label={t('Port')}
              type="number"
              className={css([
                numberInputClassName,
                { width: '7ch', textAlign: 'center' },
              ])}
              autoFocus
              maxLength={5}
              onChange={event => setPort(Number(event.target.value))}
            />
          </View>

          <View
            style={{ flexDirection: 'column', gap: 5, justifyContent: 'end' }}
          >
            <ButtonWithLoading
              variant="primary"
              style={{ padding: 10, width: '8ch' }}
              onPress={onStart}
              isLoading={isStarting}
            >
              {isRunning ? <Trans>Save</Trans> : <Trans>Start</Trans>}
            </ButtonWithLoading>
          </View>
        </View>
      </View>

      <View
        style={{
          flexDirection: 'row',
          marginTop: 20,
          gap: 15,
          flexFlow: 'row wrap',
          justifyContent: 'center',
        }}
      >
        {isRunning && (
          <Button
            variant="bare"
            style={{ color: theme.pageTextLight, margin: 5 }}
            onPress={() => navigate(-1)}
          >
            <Trans>Cancel</Trans>
          </Button>
        )}
        <Button
          variant="bare"
          style={{ color: theme.pageTextLight, margin: 5 }}
          onPress={onDontUseServer}
        >
          <Trans>Don't use a server</Trans>
        </Button>
        {onUseExternalServer && (
          <Button
            variant="bare"
            style={{ color: theme.pageTextLight, margin: 5 }}
            onPress={onUseExternalServer}
          >
            <Trans>Use an external server</Trans>
          </Button>
        )}
      </View>
    </>
  );
}
