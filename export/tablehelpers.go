package export

// tableCellOwner is the rectangle (r0,c0 upper-left corner,
// rowSpan/colSpan extent) of whichever cell owns a given grid
// position -- see tableMergeGrid's own doc comment for why a format
// needing real merge fidelity (RTF's own \clmgf/\clmrg/\clvmgf/
// \clvmrg, PDF's own colspan-only support) needs this on *every* cell
// in a table, not just the owner the way HTML's colspan/rowspan
// attribute does.
type tableCellOwner struct {
	r0, c0, rowSpan, colSpan int
}

// tableMergeGrid computes tableCellOwner for every cell in cells (one
// table's own full row-major TableRows*TableCols grid): itself, for
// an ordinary or owner cell; the actual owner's own rectangle, for a
// cell it covers. A covered cell's own TableCovered bool alone
// doesn't carry this -- only the owner ever has a real
// TableColSpan/TableRowSpan set (export.Block's own doc comment) -- so
// a caller that needs to know *where within the merge* a covered cell
// sits (RTF: same row as the owner needs \clmrg but not \clvmrg; a
// later row needs \clvmrg; PDF: same row as the owner draws no box at
// all, since the owner's own widened box already covers it, while a
// later row still draws its own blank box) has to reconstruct it.
// Computed by flooding each owner's own rectangle with its own index,
// the same two-pass "mark every covered position, then look up" shape
// TableRowHeightsAt's own row-span redistribution
// (model/layoutmetrics.go) uses for the live canvas's analogous
// problem.
func tableMergeGrid(cells []Block) []tableCellOwner {
	cols := cells[0].TableCols
	ownerIdx := make([]int, len(cells))
	for i := range cells {
		ownerIdx[i] = i
	}
	for i, cell := range cells {
		if cell.TableCovered || (cell.TableColSpan <= 1 && cell.TableRowSpan <= 1) {
			continue
		}
		for rr := cell.TableRow; rr < cell.TableRow+cell.TableRowSpan; rr++ {
			for cc := cell.TableCol; cc < cell.TableCol+cell.TableColSpan; cc++ {
				ownerIdx[rr*cols+cc] = i
			}
		}
	}
	owners := make([]tableCellOwner, len(cells))
	for i := range cells {
		o := cells[ownerIdx[i]]
		owners[i] = tableCellOwner{r0: o.TableRow, c0: o.TableCol, rowSpan: o.TableRowSpan, colSpan: o.TableColSpan}
	}
	return owners
}

// tableColumnWidths gives the width of each column of the table in a total width (in the unit of total; pxToUnit turns
// a width in CSS pixels into it): a column a person sized keeps its size and the others share what is left equally; when
// the sized ones leave the others less than a quarter of an even share, they are all scaled down to make room.
func tableColumnWidths(cells []Block, total, pxToUnit float64) []float64 {
	cols := cells[0].TableCols
	out := make([]float64, cols)
	set, sizedCols := 0.0, 0
	for c := 0; c < cols; c++ {
		if w := cells[c].TableColW * pxToUnit; w > 0 { // row 0 carries the column's width
			out[c] = w
			set += w
			sizedCols++
		}
	}
	auto := cols - sizedCols
	even := total / float64(cols)
	if auto > 0 {
		left := total - set
		if left < float64(auto)*even/4 { // the sized columns take too much: scale them
			k := (total - float64(auto)*even/4) / set
			for c := range out {
				out[c] *= k
			}
			set *= k
			left = total - set
		}
		for c := range out {
			if out[c] == 0 {
				out[c] = left / float64(auto)
			}
		}
	}
	return out
}
