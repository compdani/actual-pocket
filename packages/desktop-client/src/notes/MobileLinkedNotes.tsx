import React from 'react';
import { useTranslation } from 'react-i18next';

import { Text } from '@actual-app/components/text';
import { theme } from '@actual-app/components/theme';
import { css } from '@emotion/css';

import { addNotification } from '#notifications/notificationsSlice';
import { useDispatch } from '#redux';

import { normalizeUrl } from './linkParser';

type MobileLinkedNotesProps = {
  displayText: string;
  url: string;
  separator: string;
  isFilePath: boolean;
};

const linkStyles = css({
  color: theme.pageTextLink,
  textDecoration: 'underline',
});

export function MobileLinkedNotes({
  displayText,
  url,
  separator,
  isFilePath,
}: MobileLinkedNotesProps) {
  const dispatch = useDispatch();
  const { t } = useTranslation();

  const handleClick = async () => {
    if (isFilePath) {
      const desktop = window.Actual.desktop;
      if (desktop) {
        // The desktop app shows the file in Finder or Explorer
        await desktop.openInFileManager(url);
        return;
      }
      // The browser cannot open local files, so copy the path instead
      await navigator.clipboard.writeText(url);
      dispatch(
        addNotification({
          notification: {
            type: 'message',
            message: t('File path copied to clipboard'),
          },
        }),
      );
    } else {
      // Open URL in browser
      const normalizedUrl = normalizeUrl(url);
      window.Actual?.openURLInBrowser(normalizedUrl);
    }
  };

  return (
    <>
      <Text className={linkStyles} onClick={handleClick}>
        {displayText}
      </Text>
      {separator}
    </>
  );
}
