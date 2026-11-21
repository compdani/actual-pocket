// @ts-strict-ignore
import React, { useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';

import { Button } from '@actual-app/components/button';
import { Paragraph } from '@actual-app/components/paragraph';
import { Text } from '@actual-app/components/text';
import { theme } from '@actual-app/components/theme';
import { View } from '@actual-app/components/view';
import { send } from '@actual-app/core/platform/client/connection';

import { createBudget } from '#budgetfiles/budgetfilesSlice';
import { Link } from '#components/common/Link';
import { useRefreshLoginMethods } from '#components/ServerContext';
import { useNavigate } from '#hooks/useNavigate';
import { useDispatch } from '#redux';

import { Title, useBootstrapped } from './common';
import { ConfirmPasswordForm } from './ConfirmPasswordForm';

export function Bootstrap() {
  const { t } = useTranslation();
  const dispatch = useDispatch();
  const [error, setError] = useState(null);
  const [email, setEmail] = useState('');
  const refreshLoginMethods = useRefreshLoginMethods();

  const { checked } = useBootstrapped();
  const navigate = useNavigate();

  function getErrorMessage(error) {
    switch (error) {
      case 'invalid-email':
        return t('Enter a valid email address');
      case 'invalid-password':
        return t('Password must be at least 8 characters');
      case 'password-match':
        return t('Passwords do not match');
      case 'network-failure':
        return t('Unable to contact the server');
      default:
        return t(`An unknown error occurred: {{error}}`, { error });
    }
  }

  async function onSetPassword(password) {
    setError(null);
    if (email.trim() === '') {
      setError('invalid-email');
      return;
    }
    const { error } = await send('subscribe-bootstrap', {
      email: email.trim(),
      password,
    });

    if (error) {
      setError(error);
    } else {
      await refreshLoginMethods();
      void navigate('/login');
    }
  }

  async function onDemo() {
    await dispatch(createBudget({ demoMode: true }));
  }

  if (!checked) {
    return null;
  }

  return (
    <View style={{ maxWidth: 450 }}>
      <Title text={t('Welcome to Actual!')} />
      <Paragraph style={{ fontSize: 16, color: theme.pageTextDark }}>
        <Trans>
          Actual is a super fast privacy-focused app for managing your finances.
          To secure your data, you'll need to create the server owner account
          with an email address and a password.
        </Trans>
      </Paragraph>

      <Paragraph isLast style={{ fontSize: 16, color: theme.pageTextDark }}>
        <Trans>
          Consider opening{' '}
          <Link variant="external" to="https://actualbudget.org/docs/tour/">
            our tour
          </Link>{' '}
          in a new tab for some guidance on what to do when you've set your
          password.
        </Trans>
      </Paragraph>

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

      <ConfirmPasswordForm
        email={{ value: email, onChange: setEmail }}
        buttons={
          <Button
            variant="bare"
            style={{
              fontSize: 15,
              color: theme.pageTextLink,
              marginRight: 15,
            }}
            onPress={onDemo}
          >
            {t('Try Demo')}
          </Button>
        }
        onSetPassword={onSetPassword}
        onError={setError}
      />
    </View>
  );
}
