// @ts-strict-ignore
import React, { useEffect, useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';
import { useLocation } from 'react-router';

import { Button, ButtonWithLoading } from '@actual-app/components/button';
import { BigInput } from '@actual-app/components/input';
import { Text } from '@actual-app/components/text';
import { theme } from '@actual-app/components/theme';
import { View } from '@actual-app/components/view';
import { isWails } from '@actual-app/core/shared/environment';
import * as Platform from '@actual-app/core/shared/platform';
import type { DesktopSyncServerStatus } from '@actual-app/core/typings/window';

import { Link } from '#components/common/Link';
import { MobileBackButton } from '#components/mobile/MobileBackButton';
import { useServerURL, useSetServerURL } from '#components/ServerContext';
import { useNavigate } from '#hooks/useNavigate';
import { useDispatch, useSelector } from '#redux';
import { loggedIn, signOut } from '#users/usersSlice';

import { LocalServerConfig } from './LocalServerConfig';
import { Title } from './subscribe/common';

export function ConfigServer() {
  const { t } = useTranslation();
  const dispatch = useDispatch();
  const navigate = useNavigate();
  const location = useLocation();
  const userData = useSelector(state => state.user.data);
  const currentUrl = useServerURL();
  const setServerUrl = useSetServerURL();
  const [url, setUrl] = useState(currentUrl);
  const [prevCurrentUrl, setPrevCurrentUrl] = useState(currentUrl);
  if (currentUrl !== prevCurrentUrl) {
    setPrevCurrentUrl(currentUrl);
    setUrl(currentUrl);
  }
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [certificateMessage, setCertificateMessage] = useState<string | null>(
    null,
  );

  // The desktop app can run a server on this computer. It shows that setup
  // first unless another server is already in use, as the Electron app did.
  const desktop = window.Actual.desktop;
  const [localStatus, setLocalStatus] =
    useState<DesktopSyncServerStatus | null>(null);
  const [serverConfigView, setServerConfigView] = useState<
    'internal' | 'external' | null
  >(desktop ? null : 'external');

  useEffect(() => {
    if (!desktop) {
      return;
    }
    let isCancelled = false;
    desktop
      .getSyncServerStatus()
      .then(status => {
        if (isCancelled) {
          return;
        }
        setLocalStatus(status);
        const usesOtherServer =
          !!currentUrl && currentUrl !== `http://localhost:${status.port}`;
        setServerConfigView(
          status.available && !usesOtherServer ? 'internal' : 'external',
        );
      })
      .catch(() => {
        if (!isCancelled) {
          setServerConfigView('external');
        }
      });
    return () => {
      isCancelled = true;
    };
  }, [desktop, currentUrl]);

  async function onTrustCertificate() {
    if (!desktop) {
      return;
    }
    try {
      const message = await desktop.trustCertificate();
      if (message) {
        setError(null);
        setCertificateMessage(message);
      }
    } catch (trustError) {
      setCertificateMessage(
        trustError instanceof Error ? trustError.message : String(trustError),
      );
    }
  }

  function getErrorMessage(error: string) {
    switch (error) {
      case 'network-failure':
        return t(
          'Connection failed. If you use a self-signed certificate or were recently offline, try refreshing the page. Otherwise ensure you have HTTPS set up properly.',
        );
      default:
        return t(
          'Server does not look like an Actual server. Is it set up correctly?',
        );
    }
  }

  async function onSubmit() {
    if (url === null || url === '' || loading) {
      return;
    }

    setError(null);
    setLoading(true);

    let httpUrl = url;
    if (!url.startsWith('http://') && !url.startsWith('https://')) {
      httpUrl = 'https://' + url;
    }

    const { error } = await setServerUrl(httpUrl);
    setUrl(httpUrl);

    if (error) {
      setLoading(false);
      setError(error);
    } else {
      setLoading(false);
      await dispatch(signOut());
      void navigate('/');
    }
  }

  function onSameDomain() {
    setUrl(window.location.origin);
  }

  async function onSkip() {
    await setServerUrl(null);
    await dispatch(loggedIn());
    void navigate('/');
  }

  if (serverConfigView === null) {
    return null;
  }

  return (
    <View style={{ maxWidth: 500, marginTop: -30 }}>
      {(userData || currentUrl) && (
        <MobileBackButton
          onPress={() =>
            location.key !== 'default' ? navigate(-1) : navigate('/')
          }
          style={{
            position: 'fixed',
            top: 'calc(10px + env(safe-area-inset-top))',
            left: 10,
            margin: 0,
            zIndex: 4000,
          }}
        />
      )}
      {serverConfigView === 'internal' && desktop && localStatus && (
        <LocalServerConfig
          desktop={desktop}
          status={localStatus}
          onDoNotUseServer={onSkip}
          onUseExternalServer={() => setServerConfigView('external')}
        />
      )}
      {serverConfigView === 'external' && (
        <>
          <Title text={t('Connect to a server')} />
          <Text
            style={{
              fontSize: 16,
              color: theme.tableRowHeaderText,
              lineHeight: 1.5,
            }}
          >
            {currentUrl ? (
              <Trans>
                Existing sessions will be logged out and you will log in to this
                server. We will validate that Actual is running at this URL.
              </Trans>
            ) : (
              <Trans>
                A sync server keeps your budget up to date across all your
                devices and unlocks features like bank syncing. It is completely
                optional: Actual works great on just this device too.
              </Trans>
            )}
          </Text>
          {!currentUrl && !Platform.isNativeMobileApp && (
            <Text
              style={{
                fontSize: 16,
                color: theme.pageTextLight,
                lineHeight: 1.5,
                marginTop: 10,
              }}
            >
              <Trans>
                If you already run a server, enter its URL below. Otherwise you
                can{' '}
                <Link
                  variant="external"
                  to="https://actualbudget.org/docs/install/"
                  linkColor="purple"
                >
                  learn how to set one up
                </Link>{' '}
                and connect it whenever you are ready.
              </Trans>
            </Text>
          )}
          {error && (
            <Text
              size="large"
              style={{
                marginTop: 20,
                color: theme.errorText,
                borderRadius: 4,
              }}
            >
              {getErrorMessage(error)}
            </Text>
          )}
          {error && desktop && (
            <Text
              size="large"
              style={{ marginTop: 20, color: theme.errorText, borderRadius: 4 }}
            >
              <Trans>
                If the server is using a self-signed certificate{' '}
                <Link
                  variant="text"
                  style={{ fontSize: 15 }}
                  onClick={onTrustCertificate}
                >
                  select it here
                </Link>
                .
              </Trans>
            </Text>
          )}
          {certificateMessage && (
            <Text size="large" style={{ marginTop: 20, color: theme.pageText }}>
              {certificateMessage}
            </Text>
          )}
          <View
            style={{ display: 'flex', flexDirection: 'row', marginTop: 30 }}
          >
            <BigInput
              autoFocus
              placeholder={t('https://example.com')}
              value={url || ''}
              onChangeValue={setUrl}
              style={{ flex: 1, marginRight: 10 }}
              onEnter={onSubmit}
            />
            <ButtonWithLoading
              variant="primary"
              isLoading={loading}
              style={{ fontSize: 15 }}
              onPress={onSubmit}
            >
              <Trans>Connect</Trans>
            </ButtonWithLoading>
          </View>
          <View
            style={{
              alignItems: 'center',
              gap: 15,
              marginTop: 30,
            }}
          >
            {currentUrl ? (
              <Button
                variant="bare"
                style={{ color: theme.pageTextLink }}
                onPress={onSkip}
              >
                <Trans>Stop using a server</Trans>
              </Button>
            ) : (
              <>
                {!isWails() && !Platform.isNativeMobileApp && (
                  <Button
                    variant="bare"
                    style={{ color: theme.pageTextLink }}
                    onPress={onSameDomain}
                  >
                    <Trans>Use the current domain</Trans>
                  </Button>
                )}
                {!userData && (
                  <Button
                    variant="bare"
                    style={{ color: theme.pageTextLink }}
                    onPress={onSkip}
                  >
                    <Trans>Don't use a server</Trans>
                  </Button>
                )}
              </>
            )}
            {localStatus?.available && (
              <Button
                variant="bare"
                style={{ color: theme.pageTextLink }}
                onPress={() => setServerConfigView('internal')}
              >
                <Trans>Run a server on this computer</Trans>
              </Button>
            )}
          </View>
        </>
      )}
    </View>
  );
}
