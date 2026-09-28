import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import type { SchemaVersion, Subject } from "@/lib/api";
import { Route as SchemasIndexRoute } from "@/routes/clusters.$cluster.schemas.index";
import { Route as TopicSchemaRoute } from "@/routes/clusters.$cluster.topics.$topic.schema";

// The Schemas page and a topic's Schema tab read the same subject list and
// the same latest version. Renders both real route components against one
// QueryClient and walks between them: each resource is fetched once, and
// deleting the subject on the Schemas page is visible on the Schema tab.

const listSubjects = vi.hoisted(() => vi.fn());
const getSchemaVersion = vi.hoisted(() => vi.fn());
const deleteSubject = vi.hoisted(() => vi.fn());
const fetchClusters = vi.hoisted(() => vi.fn());

vi.mock("@/lib/api", async (importActual) => {
  const actual = await importActual<typeof import("@/lib/api")>();
  return { ...actual, listSubjects, getSchemaVersion, deleteSubject, fetchClusters };
});

const CLUSTER = "c";
const SUBJECT = "orders-value";
const TAB_PATH = `/clusters/${CLUSTER}/topics/orders/schema`;
const SCHEMAS_PATH = `/clusters/${CLUSTER}/schemas/`;

const SUBJECTS: Subject[] = [{ name: SUBJECT, versions: [1] }];
const LATEST: SchemaVersion = {
  subject: SUBJECT,
  version: 1,
  id: 7,
  schema: '{"type":"string"}',
  schemaType: "AVRO",
};

function renderRoutes() {
  const root = createRootRoute();
  const cluster = createRoute({ getParentRoute: () => root, path: "/clusters/$cluster" });
  const schemas = createRoute({ getParentRoute: () => cluster, path: "/schemas" });
  const topics = createRoute({ getParentRoute: () => cluster, path: "/topics" });
  const topic = createRoute({ getParentRoute: () => topics, path: "/$topic" });
  // Same cast as routeTree.gen.ts: update() is typed for generated trees.
  const schemasIndex = SchemasIndexRoute.update({
    id: "/",
    path: "/",
    getParentRoute: () => schemas,
  } as any);
  const topicSchema = TopicSchemaRoute.update({
    id: "/schema",
    path: "/schema",
    getParentRoute: () => topic,
  } as any);
  const routeTree = root.addChildren([
    cluster.addChildren([
      schemas.addChildren([schemasIndex]),
      topics.addChildren([topic.addChildren([topicSchema])]),
    ]),
  ]);
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [TAB_PATH] }),
  });
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 30_000 } } });
  render(
    <QueryClientProvider client={qc}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return { router, qc };
}

beforeEach(() => {
  listSubjects.mockReset().mockResolvedValue(SUBJECTS);
  getSchemaVersion.mockReset().mockResolvedValue(LATEST);
  deleteSubject.mockReset().mockResolvedValue({ deleted: [1] });
  fetchClusters.mockReset().mockResolvedValue([
    {
      name: CLUSTER,
      reachable: true,
      is_prod: false,
      auth_type: "none",
      tls: false,
      schema_registry: true,
    },
  ]);
});

afterEach(() => {
  cleanup();
  localStorage.clear();
});

describe("schema queries", () => {
  it("share the subject list and latest version between the Schema tab and the Schemas page", async () => {
    const user = userEvent.setup();
    const { router } = renderRoutes();

    expect(await screen.findByText("v1 · id 7")).toBeInTheDocument();
    expect(listSubjects).toHaveBeenCalledTimes(1);
    expect(getSchemaVersion).toHaveBeenCalledTimes(1);
    expect(getSchemaVersion).toHaveBeenLastCalledWith(CLUSTER, SUBJECT, "latest");

    await act(() => router.history.push(SCHEMAS_PATH));
    await user.click(await screen.findByRole("button", { name: new RegExp(`^${SUBJECT}`) }));
    expect(await screen.findByText("Latest: v1")).toBeInTheDocument();

    // Both views were served from the entries the Schema tab loaded.
    expect(listSubjects).toHaveBeenCalledTimes(1);
    expect(getSchemaVersion).toHaveBeenCalledTimes(1);
  });

  it("show a subject deleted on the Schemas page as gone on the Schema tab", async () => {
    const user = userEvent.setup();
    const { router, qc } = renderRoutes();
    expect(await screen.findByText("v1 · id 7")).toBeInTheDocument();

    await act(() => router.history.push(SCHEMAS_PATH));
    listSubjects.mockResolvedValue([]);
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    await user.click(await screen.findByRole("button", { name: `Delete subject ${SUBJECT}` }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByRole("textbox"), SUBJECT);
    await user.click(within(dialog).getByRole("button", { name: "Delete subject" }));

    await waitFor(() => expect(deleteSubject).toHaveBeenCalledWith(CLUSTER, SUBJECT, false));
    await waitFor(() => expect(invalidate).toHaveBeenCalledTimes(2));
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["schemas", CLUSTER] });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["schema", CLUSTER, SUBJECT] });
    // The cached latest version is marked stale along with the list.
    expect(qc.getQueryState(["schema", CLUSTER, SUBJECT, "latest"])?.isInvalidated).toBe(true);

    await act(() => router.history.push(TAB_PATH));
    expect(await screen.findByText("No schema registered for this topic")).toBeInTheDocument();
    expect(screen.queryByText("v1 · id 7")).not.toBeInTheDocument();
  });
});
