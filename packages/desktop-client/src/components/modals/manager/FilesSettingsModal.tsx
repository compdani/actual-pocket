import React from 'react';
import { Trans, useTranslation } from 'react-i18next';

import { Button } from '@actual-app/components/button';
import { View } from '@actual-app/components/view';

import { Modal, ModalCloseButton, ModalHeader } from '#components/common/Modal';

import { CertificateSettings } from './CertificateSettings';
import { FileLocationSettings } from './FileLocationSettings';

export function FilesSettingsModal() {
  const { t } = useTranslation();
  const desktop = window.Actual.desktop;

  return (
    <Modal name="files-settings">
      {({ state }) => (
        <>
          <ModalHeader
            title={t('Settings')}
            rightContent={<ModalCloseButton onPress={() => state.close()} />}
          />
          <View
            style={{
              padding: 15,
              gap: 15,
              paddingTop: 0,
              paddingBottom: 25,
              maxWidth: 550,
              lineHeight: '1.5em',
            }}
          >
            {desktop && <FileLocationSettings desktop={desktop} />}
            {desktop && <CertificateSettings desktop={desktop} />}
            <Button
              variant="primary"
              style={{
                padding: '10px 30px',
                fontSize: 14,
                alignSelf: 'center',
              }}
              onPress={() => state.close()}
            >
              <Trans>OK</Trans>
            </Button>
          </View>
        </>
      )}
    </Modal>
  );
}
