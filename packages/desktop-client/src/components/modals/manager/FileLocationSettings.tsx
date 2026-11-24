import React, { useEffect, useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';

import { Button } from '@actual-app/components/button';
import { SvgPencil1 } from '@actual-app/components/icons/v2';
import { Text } from '@actual-app/components/text';
import { View } from '@actual-app/components/view';
import type { DesktopHost } from '@actual-app/core/typings/window';

import { DirectoryDisplay } from '#components/common/DirectoryDisplay';
import { pushModal } from '#modals/modalsSlice';
import { useDispatch } from '#redux';

import { sectionStyle } from './settingsSectionStyle';

type FileLocationSettingsProps = {
  desktop: DesktopHost;
};

export function FileLocationSettings({ desktop }: FileLocationSettingsProps) {
  const [documentDir, setDocumentDir] = useState<string | null>(null);
  const { t } = useTranslation();
  const dispatch = useDispatch();

  useEffect(() => {
    let isCancelled = false;
    void desktop.getDocumentDir().then(dir => {
      if (!isCancelled) {
        setDocumentDir(dir);
      }
    });
    return () => {
      isCancelled = true;
    };
  }, [desktop]);

  async function onChooseDocumentDir() {
    const chosenDirectory = await desktop.chooseFolder(
      t('Choose a folder for your budgets'),
    );
    if (chosenDirectory && documentDir && chosenDirectory !== documentDir) {
      dispatch(
        pushModal({
          modal: {
            name: 'confirm-change-document-dir',
            options: {
              currentBudgetDirectory: documentDir,
              newDirectory: chosenDirectory,
            },
          },
        }),
      );
    }
  }

  return (
    <View style={sectionStyle}>
      <Text>
        <Trans>
          <strong>Actual's data directory</strong>{' '}
          <small style={{ marginLeft: '0.5rem' }}>
            <i>where your files are stored</i>
          </small>
        </Trans>
      </Text>
      <View style={{ flexDirection: 'row', gap: '0.5rem', width: '100%' }}>
        <DirectoryDisplay directory={documentDir ?? ''} />
        <Button onPress={onChooseDocumentDir} aria-label={t('Change location')}>
          <SvgPencil1 style={{ width: 11, height: 11 }} />
        </Button>
      </View>
      <Button variant="bare" onPress={() => void desktop.revealDocumentDir()}>
        <Trans>Show in file manager</Trans>
      </Button>
    </View>
  );
}
