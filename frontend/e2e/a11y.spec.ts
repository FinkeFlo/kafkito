import { test, expect, type Page } from "@playwright/test";
import { expectNoBlockingA11yViolations, expectTheme, pinTheme, THEMES } from "./fixtures/axe";

const CLUSTER = process.env.KAFKITO_E2E_CLUSTER ?? "local";
const TOPIC = "e2e-walk-target";
const GROUP = "e2e-idle-group";
// Seeded by seed.sh into the e2e Schema Registry.
const SUBJECT = "e2e-avro-orders-value";

const c = encodeURIComponent(CLUSTER);
const topicPath = (tab: string) => `/clusters/${c}/topics/${encodeURIComponent(TOPIC)}/${tab}`;

type Route = {
  name: string;
  path: string;
  // Waits until the route has rendered its data, so axe scans the real page
  // and not a loading skeleton.
  ready: (page: Page) => Promise<void>;
};

const h1 = (page: Page, name: string) =>
  expect(page.getByRole("heading", { level: 1, name, exact: true })).toBeVisible();

const ROUTES: Route[] = [
  {
    name: "topics",
    path: `/clusters/${c}/topics`,
    ready: async (page) => {
      await h1(page, "Topics");
      await expect(page.getByRole("row", { name: new RegExp(TOPIC) }).first()).toBeVisible();
    },
  },
  {
    name: "topic-messages",
    path: topicPath("messages"),
    ready: async (page) => {
      await h1(page, TOPIC);
      await expect(page.getByText("seed-message-1", { exact: true }).first()).toBeVisible();
    },
  },
  {
    name: "topic-timeline",
    path: topicPath("timeline"),
    ready: async (page) => {
      await h1(page, TOPIC);
      await expect(page.getByRole("img", { name: /message count per time slot/i })).toBeVisible();
    },
  },
  {
    name: "topic-produce",
    path: topicPath("produce"),
    ready: async (page) => {
      await h1(page, TOPIC);
      await expect(page.getByRole("button", { name: /^produce/i }).first()).toBeVisible();
    },
  },
  {
    name: "topic-configs",
    path: topicPath("configs"),
    ready: async (page) => {
      await h1(page, TOPIC);
      await expect(page.getByText("retention.ms").first()).toBeVisible();
    },
  },
  {
    name: "topic-consumers",
    path: topicPath("consumers"),
    ready: async (page) => {
      await h1(page, TOPIC);
      await expect(page.getByRole("button", { name: "Create consumer group" })).toBeVisible();
    },
  },
  {
    name: "topic-schema",
    path: topicPath("schema"),
    ready: (page) => h1(page, TOPIC),
  },
  {
    name: "groups",
    path: `/clusters/${c}/groups`,
    ready: async (page) => {
      await h1(page, "Consumer groups");
      await expect(page.getByRole("button", { name: new RegExp(GROUP) })).toBeVisible();
    },
  },
  {
    name: "group-detail",
    path: `/clusters/${c}/groups?group=${encodeURIComponent(GROUP)}`,
    ready: async (page) => {
      await h1(page, "Consumer groups");
      await expect(page.getByText("Offsets", { exact: true })).toBeVisible();
    },
  },
  {
    name: "schemas",
    path: `/clusters/${c}/schemas`,
    ready: async (page) => {
      await h1(page, "Schemas");
      await expect(page.getByRole("button", { name: new RegExp(`^${SUBJECT}`) })).toBeVisible();
    },
  },
  {
    name: "security-acls",
    path: `/clusters/${c}/security/acls`,
    ready: async (page) => {
      await h1(page, "Security");
      await expect(page.getByRole("button", { name: "+ New rule" })).toBeVisible();
    },
  },
  {
    name: "security-users",
    path: `/clusters/${c}/security/users`,
    ready: (page) => h1(page, "Security"),
  },
  {
    name: "brokers",
    path: `/clusters/${c}/brokers`,
    ready: async (page) => {
      await h1(page, "Brokers");
      await expect(page.getByRole("table")).toBeVisible();
    },
  },
  {
    name: "settings-clusters",
    path: "/settings/clusters",
    ready: (page) => expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible(),
  },
];

// Interaction states that only exist after a click: dialogs, expanded rows
// and panels. Each one starts from a route above and opens the state the way
// a user would.
type State = Route & { open: (page: Page) => Promise<void> };

function route(name: string): Route {
  const found = ROUTES.find((r) => r.name === name);
  if (!found) throw new Error(`unknown route ${name}`);
  return found;
}

const STATES: State[] = [
  {
    ...route("topics"),
    name: "topic-create-modal",
    open: async (page) => {
      await page.getByRole("button", { name: /^\+ New topic$/ }).click();
      await expect(page.getByRole("dialog")).toBeVisible();
    },
  },
  {
    ...route("topic-messages"),
    name: "message-expanded",
    open: async (page) => {
      await page.getByTestId("message-row").first().getByRole("button").first().click();
      await expect(page.getByRole("button", { name: "Copy value", exact: true })).toBeVisible();
    },
  },
  {
    ...route("topic-messages"),
    name: "message-search-panel",
    open: async (page) => {
      await page.getByRole("button", { name: "Search", exact: true }).click();
      await expect(page.getByLabel("Mode")).toBeVisible();
    },
  },
  {
    ...route("topic-messages"),
    name: "bulk-copy-panel",
    open: async (page) => {
      await page.getByRole("button", { name: /Copy messages to another cluster/ }).click();
      await expect(page.getByPlaceholder("topic-name")).toBeVisible();
    },
  },
  {
    ...route("topic-consumers"),
    name: "create-group-modal",
    open: async (page) => {
      await page.getByRole("button", { name: "Create consumer group" }).click();
      await expect(page.getByRole("dialog", { name: /create consumer group/i })).toBeVisible();
    },
  },
  {
    ...route("group-detail"),
    name: "reset-offsets-modal",
    open: async (page) => {
      await page.getByRole("button", { name: /^reset offsets/i }).click();
      await expect(page.getByRole("dialog", { name: /reset offsets/i })).toBeVisible();
    },
  },
  {
    ...route("security-acls"),
    name: "acl-create-modal",
    open: async (page) => {
      await page.getByRole("button", { name: "+ New rule" }).click();
      await expect(page.getByRole("dialog", { name: /new acl on/i })).toBeVisible();
    },
  },
  {
    ...route("security-users"),
    name: "scram-user-modal",
    open: async (page) => {
      await page.getByRole("button", { name: "+ New User" }).click();
      await expect(page.getByRole("dialog", { name: "Create / Update SCRAM User" })).toBeVisible();
    },
  },
  {
    ...route("settings-clusters"),
    name: "add-cluster-dialog",
    open: async (page) => {
      await page.getByRole("button", { name: "Add cluster" }).click();
      await expect(page.getByRole("dialog", { name: "Add private cluster" })).toBeVisible();
    },
  },
  // Form error states: the field carries aria-invalid and the visible
  // error text is its description.
  {
    ...route("topic-produce"),
    name: "produce-invalid-json",
    open: async (page) => {
      await page.getByRole("textbox", { name: "Value", exact: true }).fill("{not json");
      await page.getByRole("button", { name: "Format JSON" }).click();
      await expect(page.getByText(/^Value is not valid JSON/)).toBeVisible();
    },
  },
  {
    ...route("security-users"),
    name: "scram-user-invalid-iterations",
    open: async (page) => {
      await page.getByRole("button", { name: "+ New User" }).click();
      const dialog = page.getByRole("dialog", { name: "Create / Update SCRAM User" });
      await dialog.getByRole("spinbutton").fill("100");
      await expect(dialog.getByText("Iterations must be between 4096 and 16384.")).toBeVisible();
    },
  },
  {
    ...route("group-detail"),
    name: "reset-offsets-invalid-offset",
    open: async (page) => {
      await page.getByRole("button", { name: /^reset offsets/i }).click();
      const dialog = page.getByRole("dialog", { name: /reset offsets/i });
      await dialog.getByRole("combobox", { name: "Strategy" }).selectOption("offset");
      await dialog.getByRole("textbox", { name: /^Offset/ }).fill("abc");
      await expect(dialog.getByText("Enter a numeric offset.")).toBeVisible();
    },
  },
  {
    ...route("topic-consumers"),
    name: "create-group-invalid-offset",
    open: async (page) => {
      await page.getByRole("button", { name: "Create consumer group" }).click();
      const dialog = page.getByRole("dialog", { name: /create consumer group/i });
      await dialog.getByRole("combobox", { name: "Strategy" }).selectOption("offset");
      await dialog.getByRole("textbox", { name: /^Offset/ }).fill("1.5");
      await expect(dialog.getByText("Enter a whole-number offset.")).toBeVisible();
    },
  },
  {
    ...route("topics"),
    name: "user-menu",
    open: async (page) => {
      await page.getByRole("button", { name: /^Account menu for / }).click();
      await expect(page.getByLabel("Account and settings")).toBeVisible();
    },
  },
];

for (const theme of THEMES) {
  test.describe(`axe (${theme} theme)`, () => {
    test.beforeEach(async ({ page }) => {
      await pinTheme(page, theme);
    });

    for (const route of ROUTES) {
      test(`${route.name} has no moderate, serious or critical violations`, async ({
        page,
      }, testInfo) => {
        await page.goto(route.path);
        await expectTheme(page, theme);
        await route.ready(page);
        await expectNoBlockingA11yViolations(page, testInfo, `${route.name}-${theme}`);
      });
    }

    for (const state of STATES) {
      test(`${state.name} has no moderate, serious or critical violations`, async ({
        page,
      }, testInfo) => {
        await page.goto(state.path);
        await expectTheme(page, theme);
        await state.ready(page);
        await state.open(page);
        await expectNoBlockingA11yViolations(page, testInfo, `${state.name}-${theme}`);
      });
    }

    test("command palette has no moderate, serious or critical violations", async ({
      page,
    }, testInfo) => {
      await page.goto(`/clusters/${c}/topics`);
      await expectTheme(page, theme);
      await h1(page, "Topics");
      await page.getByRole("button", { name: /find anything/i }).click();
      const palette = page.getByRole("dialog", { name: "Command palette" });
      const input = palette.getByRole("textbox", { name: "Find anything" });
      await expect(input).toBeFocused();
      await input.fill(TOPIC.slice(0, 6));
      await expect(page.getByText(TOPIC, { exact: true }).first()).toBeVisible();
      await expectNoBlockingA11yViolations(page, testInfo, `command-palette-${theme}`);
    });
  });
}
