import type { components } from "../lib/api.gen";

// Shape of GET /user-api/currentUser (built-in @sap/approuter endpoint).
export interface CurrentUser {
  firstname: string;
  lastname: string;
  email: string;
  name: string;
  displayName: string;
}

// Shape of GET /api/v1/me, generated from api/openapi.yaml.
export type Me = components["schemas"]["MeResponse"];
