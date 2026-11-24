import React, { useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';

import { Button, ButtonWithLoading } from '@actual-app/components/button';
import { Text } from '@actual-app/components/text';
import { theme } from '@actual-app/components/theme';
import { View } from '@actual-app/components/view';

import { Information } from '#components/alerts';
import { DirectoryDisplay } from '#components/common/DirectoryDisplay';
import { Modal, ModalCloseButton, ModalHeader } from '#components/common/Modal';
import { Checkbox } from '#components/forms';

type ConfirmChangeDocumentDirModalProps = {
  currentBudgetDirectory: string;
  newDirectory: string;
};

export function ConfirmChangeDocumentDirModal({
  currentBudgetDirectory,
  newDirectory,
}: ConfirmChangeDocumentDirModalProps) {
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [moveFiles, setMoveFiles] = useState(false);
  const { t } = useTranslation();

  async function changeDirectory() {
    const desktop = window.Actual.desktop;
    if (!desktop) {
      return;
    }
    setError('');
    setLoading(true);
    try {
      if (moveFiles) {
        await desktop.moveBudgetDirectory(newDirectory);
      } else {
        await desktop.setDocumentDir(newDirectory);
      }
      // The budget engine lists the new folder when it starts again
      window.Actual.relaunch();
    } catch (changeError) {
      console.error('There was an error changing your directory', changeError);
      setError(
        t(
          'There was an error changing your directory, please check the directory and try again. {{reason}}',
          {
            reason:
              changeError instanceof Error
                ? changeError.message
                : String(changeError),
          },
        ),
      );
      setLoading(false);
    }
  }

  return (
    <Modal name="confirm-change-document-dir">
      {({ state }) => (
        <>
          <ModalHeader
            title={t('Are you sure?')}
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
            <View
              style={{
                gap: 15,
                backgroundColor: theme.pillBackground,
                alignSelf: 'flex-start',
                alignItems: 'flex-start',
                padding: 15,
                borderRadius: 4,
                border: '1px solid ' + theme.pillBorderDark,
                width: '100%',
              }}
            >
              <Text>
                <Trans>
                  You are about to change Actual's data directory from:
                </Trans>
              </Text>
              <DirectoryDisplay directory={currentBudgetDirectory} />
              <Text>
                <Trans>To:</Trans>
              </Text>
              <DirectoryDisplay directory={newDirectory} />
              <label
                htmlFor="moveFiles"
                style={{
                  userSelect: 'none',
                  flexDirection: 'row',
                  gap: '0.5rem',
                  display: 'flex',
                  alignItems: 'center',
                }}
              >
                <Checkbox
                  id="moveFiles"
                  name="moveFiles"
                  checked={moveFiles}
                  onChange={() => setMoveFiles(!moveFiles)}
                />
                <Trans>Move files to new directory</Trans>
              </label>
              {moveFiles ? (
                <Information style={{ padding: 0 }}>
                  <Trans>
                    Nothing in the destination folder is overwritten: the move
                    stops if it already has a budget with the same name.
                  </Trans>
                </Information>
              ) : (
                <Information style={{ padding: 0 }}>
                  <Trans>
                    Your files won't be moved. You can manually move them to the
                    folder.
                  </Trans>
                </Information>
              )}

              {error && <Text style={{ color: theme.errorText }}>{error}</Text>}
            </View>
            <View
              style={{
                gap: '1rem',
                flexDirection: 'row',
                justifyContent: 'center',
              }}
            >
              <Button
                variant="normal"
                style={{ padding: '10px 30px', fontSize: 14 }}
                onPress={() => state.close()}
              >
                <Trans>Cancel</Trans>
              </Button>
              <ButtonWithLoading
                variant="primary"
                isLoading={loading}
                style={{ padding: '10px 30px', fontSize: 14 }}
                onPress={changeDirectory}
              >
                <Trans>Change directory</Trans>
              </ButtonWithLoading>
            </View>
          </View>
        </>
      )}
    </Modal>
  );
}
