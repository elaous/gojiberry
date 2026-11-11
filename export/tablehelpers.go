package export

type tableCellOwner struct {
	r0, c0, rowSpan, colSpan int
}

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
