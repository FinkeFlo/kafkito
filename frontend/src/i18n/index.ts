import i18n from "i18next";
import { initReactI18next } from "react-i18next";

import enCommon from "./locales/en/common.json";
import enTopics from "./locales/en/topics.json";
import enGroups from "./locales/en/groups.json";
import enSchemas from "./locales/en/schemas.json";
import enAcls from "./locales/en/acls.json";
import enUsers from "./locales/en/users.json";
import enErrors from "./locales/en/errors.json";
import enHome from "./locales/en/home.json";
import enPalette from "./locales/en/palette.json";

export const defaultNS = "common";
export const namespaces = [
  "common",
  "topics",
  "groups",
  "schemas",
  "acls",
  "users",
  "errors",
  "home",
  "palette",
] as const;

export const resources = {
  en: {
    common: enCommon,
    topics: enTopics,
    groups: enGroups,
    schemas: enSchemas,
    acls: enAcls,
    users: enUsers,
    errors: enErrors,
    home: enHome,
    palette: enPalette,
  },
} as const;

void i18n.use(initReactI18next).init({
  lng: "en",
  fallbackLng: "en",
  defaultNS,
  ns: namespaces as unknown as string[],
  resources,
  interpolation: {
    escapeValue: false,
  },
  react: {
    useSuspense: false,
  },
  returnNull: false,
});

export default i18n;
