export type NewUserEntity = {
  userName: string;
  displayName: string;
  role: string;
  enabled: boolean;
  /**
   * Optional password to set for the user. Leave it out to keep an existing
   * user's password or to create an OAuth2-only user.
   */
  password?: string;
};

export type UserEntity = {
  id: string;
  owner: boolean;
} & NewUserEntity;

export type UserEntityDropdown = {
  userId: string;
  userName: string;
  displayName?: string;
};

export type UserAvailable = {
  userId: string;
  displayName?: string;
  userName: string;
  haveAccess?: number;
  owner?: number;
};
