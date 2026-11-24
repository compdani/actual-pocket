import { theme } from '@actual-app/components/theme';

// A bordered box around one setting in the desktop file settings
export const sectionStyle = {
  gap: 15,
  backgroundColor: theme.pillBackground,
  alignSelf: 'flex-start',
  alignItems: 'flex-start',
  padding: 15,
  borderRadius: 4,
  border: '1px solid ' + theme.pillBorderDark,
  width: '100%',
} as const;
