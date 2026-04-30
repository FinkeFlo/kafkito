// Shape of GET /user-api/currentUser (built-in @sap/approuter endpoint).
export interface CurrentUser {
  firstname: string;
  lastname: string;
  email: string;
  name: string;
  displayName: string;
}

// Shape of GET /api/v1/me (kafkito Go backend, JWT-aware).
export interface Me {
  user: string;
  email: string;
  tenant: string;
  scopes: string[];
  roles: string[];
  permissions: Record<string, string[]>;
  anonymous: boolean;
  jwt: boolean;
  rbac_enabled: boolean;
}
