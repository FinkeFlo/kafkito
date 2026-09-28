import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { DataTable, DataTableRow } from "./DataTable";

type Row = { id: string; state: string };
const rows: Row[] = [
  { id: "alpha", state: "Stable" },
  { id: "beta", state: "Empty" },
];

function renderColumnTable(onRowClick = vi.fn(), expanded?: string) {
  render(
    <DataTable<Row>
      columns={[
        { id: "id", header: "ID", cell: (r) => r.id },
        { id: "state", header: "State", cell: (r) => <span>{r.state}</span> },
      ]}
      rows={rows}
      rowKey={(r) => r.id}
      onRowClick={onRowClick}
      isRowExpanded={(r) => r.id === expanded}
    />,
  );
  return onRowClick;
}

describe("DataTable clickable rows", () => {
  it("keeps rows as rows and puts a button in the primary cell", () => {
    renderColumnTable(vi.fn(), "beta");
    const [, alphaRow, betaRow] = screen.getAllByRole("row");
    for (const row of [alphaRow, betaRow]) {
      expect(row).not.toHaveAttribute("role");
      expect(row).not.toHaveAttribute("tabindex");
    }
    expect(screen.getByRole("button", { name: "alpha" })).toHaveAttribute("aria-expanded", "false");
    expect(screen.getByRole("button", { name: "beta" })).toHaveAttribute("aria-expanded", "true");
  });

  it("activates once from the button and forwards clicks on other cells", () => {
    const onRowClick = renderColumnTable();
    fireEvent.click(screen.getByRole("button", { name: "alpha" }));
    expect(onRowClick).toHaveBeenCalledTimes(1);
    expect(onRowClick).toHaveBeenLastCalledWith(rows[0]);

    fireEvent.click(screen.getByText("Empty"));
    expect(onRowClick).toHaveBeenCalledTimes(2);
    expect(onRowClick).toHaveBeenLastCalledWith(rows[1]);
  });

  it("composition rows forward clicks to the marked control, not to other controls", () => {
    const primary = vi.fn();
    const other = vi.fn();
    render(
      <DataTable>
        <tbody>
          <DataTableRow clickable>
            <td>
              <button type="button" data-row-primary="" onClick={primary}>
                open
              </button>
            </td>
            <td>plain</td>
            <td>
              <button type="button" onClick={other}>
                delete
              </button>
            </td>
          </DataTableRow>
        </tbody>
      </DataTable>,
    );
    fireEvent.click(screen.getByText("plain"));
    expect(primary).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole("button", { name: "delete" }));
    expect(other).toHaveBeenCalledTimes(1);
    expect(primary).toHaveBeenCalledTimes(1);
  });
});
