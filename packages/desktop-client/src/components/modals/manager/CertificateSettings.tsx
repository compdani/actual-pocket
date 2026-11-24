import React, { useState } from 'react';
import { Trans } from 'react-i18next';

import { Button } from '@actual-app/components/button';
import { Text } from '@actual-app/components/text';
import { View } from '@actual-app/components/view';
import type { DesktopHost } from '@actual-app/core/typings/window';

import { sectionStyle } from './settingsSectionStyle';

type CertificateSettingsProps = {
  desktop: DesktopHost;
};

export function CertificateSettings({ desktop }: CertificateSettingsProps) {
  const [message, setMessage] = useState<string | null>(null);

  async function onTrustCertificate() {
    try {
      const result = await desktop.trustCertificate();
      if (result) {
        setMessage(result);
      }
    } catch (error) {
      setMessage(error instanceof Error ? error.message : String(error));
    }
  }

  return (
    <View style={sectionStyle}>
      <Text>
        <Trans>
          <strong>Server self-signed certificate</strong>{' '}
          <small style={{ marginLeft: '0.5rem' }}>
            <i>enables a secure connection</i>
          </small>
        </Trans>
      </Text>
      <Button onPress={onTrustCertificate}>
        <Trans>Trust a certificate…</Trans>
      </Button>
      {message && <Text>{message}</Text>}
    </View>
  );
}
