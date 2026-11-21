// @ts-strict-ignore
import React, { useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';

import { ButtonWithLoading } from '@actual-app/components/button';
import { useResponsive } from '@actual-app/components/hooks/useResponsive';
import { BigInput } from '@actual-app/components/input';
import { Text } from '@actual-app/components/text';
import { theme } from '@actual-app/components/theme';
import { View } from '@actual-app/components/view';
import { send } from '@actual-app/core/platform/client/connection';
import { isElectron } from '@actual-app/core/shared/environment';
import * as Platform from '@actual-app/core/shared/platform';

import { useAvailableLoginMethods } from '#components/ServerContext';
import { useDispatch } from '#redux';
import { loggedIn } from '#users/usersSlice';

import { Title, useBootstrapped } from './common';

// Electron and the native mobile apps sign in with the provider in the system
// browser, so they need a return URL that lands back in the app instead of the
// web view's own origin. The mobile apps register the `actualbudget://` scheme
// (see useMobileOpenIdCallback for the receiving side); `localhost` is the host
// the server accepts for app-local return URLs. Navigating the web view to the
// provider is what opens the system browser there: Capacitor hands any
// top-level navigation to an outside host to the OS and keeps the web view on
// this page. (window.open would be popup-blocked, since the click's user
// gesture has expired by the time the sign-in request resolves.)
async function getOpenIdReturnUrl() {
  if (isElectron()) {
    return window.Actual.startOAuthServer();
  }
  if (Platform.isNativeMobileApp) {
    return 'actualbudget://localhost';
  }
  return window.location.origin;
}

function PasswordLogin({ setError, dispatch }) {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const { t } = useTranslation();
  const { isNarrowWidth } = useResponsive();

  async function onSubmitPassword() {
    if (email === '' || password === '' || loading) {
      return;
    }

    setError(null);
    setLoading(true);
    const { error } = await send('subscribe-sign-in', {
      email,
      password,
      loginMethod: 'password',
    });
    setLoading(false);

    if (error) {
      setError(error);
    } else {
      dispatch(loggedIn());
    }
  }

  return (
    <View style={{ marginTop: 5, gap: '1rem' }}>
      <BigInput
        autoFocus
        placeholder={t('Email')}
        type="email"
        autoComplete="username"
        onChangeValue={setEmail}
        onEnter={onSubmitPassword}
      />
      <View
        style={{
          flexDirection: isNarrowWidth ? 'column' : 'row',
          gap: '1rem',
        }}
      >
        <BigInput
          placeholder={t('Password')}
          type="password"
          autoComplete="current-password"
          onChangeValue={setPassword}
          style={{ flex: 1 }}
          onEnter={onSubmitPassword}
        />
        <ButtonWithLoading
          variant="primary"
          isLoading={loading}
          style={{
            fontSize: 15,
            width: isNarrowWidth ? '100%' : 170,
            ...(isNarrowWidth ? { padding: 10 } : null),
          }}
          onPress={onSubmitPassword}
        >
          <Trans>Sign in</Trans>
        </ButtonWithLoading>
      </View>
    </View>
  );
}

// One button per OAuth2 provider enabled on the `users` collection in the
// PocketBase admin. The server drives the flow and redirects back to
// `returnUrl/openid-cb?token=…`.
function OpenIdLogin({ setError }) {
  const { t } = useTranslation();
  const loginMethods = useAvailableLoginMethods();
  const [loadingProvider, setLoadingProvider] = useState<string | null>(null);

  const providers = loginMethods.filter(
    method => method.method === 'openid' && method.active,
  );

  async function signInWith(provider: string) {
    setError(null);
    setLoadingProvider(provider);
    const { error, redirectUrl } = await send('subscribe-sign-in', {
      loginMethod: 'openid',
      provider,
      returnUrl: await getOpenIdReturnUrl(),
    });
    setLoadingProvider(null);

    if (error) {
      setError(error);
      return;
    }

    if (isElectron()) {
      window.Actual?.openURLInBrowser(redirectUrl);
    } else {
      window.location.assign(redirectUrl);
    }
  }

  if (providers.length === 0) {
    return null;
  }

  return (
    <View style={{ marginTop: 15, gap: '0.5rem' }}>
      {providers.map(provider => (
        <ButtonWithLoading
          key={provider.provider ?? provider.displayName}
          variant="primary"
          isLoading={loadingProvider === provider.provider}
          isDisabled={loadingProvider !== null}
          style={{ fontSize: 15 }}
          onPress={() => signInWith(provider.provider)}
        >
          {t('Sign in with {{provider}}', {
            provider: provider.displayName,
          })}
        </ButtonWithLoading>
      ))}
    </View>
  );
}

export function Login() {
  const { t } = useTranslation();

  const dispatch = useDispatch();
  const [error, setError] = useState(null);
  const { checked } = useBootstrapped();
  const loginMethods = useAvailableLoginMethods();

  // Older servers only report the password method, so fall back to it when the
  // list is empty.
  const passwordEnabled =
    loginMethods.length === 0 ||
    loginMethods.some(method => method.method === 'password' && method.active);

  function getErrorMessage(error) {
    switch (error) {
      case 'invalid-password':
        return t('Invalid email or password');
      case 'user-disabled':
        return t('This user is disabled');
      case 'email-required':
        return t('Email is required');
      case 'provider-not-configured':
        return t('This login provider is not configured on the server');
      case 'too-many-requests':
        return t('Too many attempts. Please try again later.');
      case 'network-failure':
        return t('Unable to contact the server');
      case 'internal-error':
        return t('Internal error');
      default:
        return t(`An unknown error occurred: {{error}}`, { error });
    }
  }

  if (!checked) {
    return null;
  }

  return (
    <View style={{ maxWidth: 450, marginTop: -30, color: theme.pageText }}>
      <Title text={t('Sign in to this Actual instance')} />

      {passwordEnabled && (
        <PasswordLogin setError={setError} dispatch={dispatch} />
      )}

      <OpenIdLogin setError={setError} />

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
    </View>
  );
}
