package main

// Lecteur .xlsx minimal (sans dépendance) : valeurs brutes des cellules et
// cibles des liens hypertexte. Suffisant pour importer un tableau de suivi.

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"path"
	"strconv"
	"strings"
)

type sheetTable struct {
	Name  string
	Rows  [][]string
	Links map[[2]int]string // [ligne, colonne] (base 0) -> cible du lien
}

func readXLSX(data []byte) ([]sheetTable, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("fichier .xlsx illisible")
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[strings.TrimPrefix(f.Name, "/")] = f
	}
	read := func(name string) ([]byte, error) {
		f := files[name]
		if f == nil {
			return nil, errors.New("absent : " + name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, 200<<20))
	}

	wb, err := read("xl/workbook.xml")
	if err != nil {
		return nil, errors.New("ce n'est pas un classeur Excel (.xlsx)")
	}
	sheets := parseWorkbookSheets(wb)
	rels := map[string]string{}
	if b, err := read("xl/_rels/workbook.xml.rels"); err == nil {
		rels = parseRels(b)
	}
	var shared []string
	if b, err := read("xl/sharedStrings.xml"); err == nil {
		shared = parseSharedStrings(b)
	}

	var out []sheetTable
	for _, sh := range sheets {
		target := rels[sh.rid]
		if target == "" {
			continue
		}
		p := resolvePath("xl", target)
		b, err := read(p)
		if err != nil {
			continue
		}
		var sheetRels map[string]string
		if rb, err := read(path.Join(path.Dir(p), "_rels", path.Base(p)+".rels")); err == nil {
			sheetRels = parseRels(rb)
		}
		t := parseSheet(b, shared, sheetRels)
		t.Name = sh.name
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil, errors.New("aucune feuille lisible dans le classeur")
	}
	return out, nil
}

func resolvePath(base, target string) string {
	if strings.HasPrefix(target, "/") {
		return strings.TrimPrefix(target, "/")
	}
	return path.Clean(path.Join(base, target))
}

type wbSheet struct{ name, rid string }

// relID lit l'attribut r:id quel que soit le préfixe/namespace utilisé.
func relID(attrs []xml.Attr) string {
	for _, a := range attrs {
		if a.Name.Local == "id" && a.Name.Space != "" {
			return a.Value
		}
	}
	return ""
}

func attr(attrs []xml.Attr, name string) string {
	for _, a := range attrs {
		if a.Name.Local == name && a.Name.Space == "" {
			return a.Value
		}
	}
	return ""
}

func parseWorkbookSheets(b []byte) []wbSheet {
	var out []wbSheet
	d := xml.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "sheet" {
			out = append(out, wbSheet{name: attr(se.Attr, "name"), rid: relID(se.Attr)})
		}
	}
	return out
}

func parseRels(b []byte) map[string]string {
	out := map[string]string{}
	d := xml.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "Relationship" {
			out[attr(se.Attr, "Id")] = attr(se.Attr, "Target")
		}
	}
	return out
}

// parseSharedStrings concatène les <t> de chaque <si> (texte enrichi compris),
// en ignorant les annotations phonétiques <rPh>.
func parseSharedStrings(b []byte) []string {
	var out []string
	d := xml.NewDecoder(bytes.NewReader(b))
	var cur strings.Builder
	inSI, inT, inPh := false, false, 0
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI = true
				cur.Reset()
			case "rPh":
				inPh++
			case "t":
				inT = true
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "si":
				inSI = false
				out = append(out, cur.String())
			case "rPh":
				inPh--
			case "t":
				inT = false
			}
		case xml.CharData:
			if inSI && inT && inPh == 0 {
				cur.Write(t)
			}
		}
	}
	return out
}

// cellRef : "C7" -> (6, 2).
func cellRef(ref string) (row, col int, ok bool) {
	i := 0
	for i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z' {
		col = col*26 + int(ref[i]-'A'+1)
		i++
	}
	if i == 0 || i == len(ref) {
		return 0, 0, false
	}
	r, err := strconv.Atoi(ref[i:])
	if err != nil {
		return 0, 0, false
	}
	return r - 1, col - 1, true
}

func parseSheet(b []byte, shared []string, rels map[string]string) sheetTable {
	t := sheetTable{Links: map[[2]int]string{}}
	cells := map[[2]int]string{}
	maxRow, maxCol := -1, -1

	d := xml.NewDecoder(bytes.NewReader(b))
	var (
		curRow, curCol = -1, -1
		cType, cRef    string
		val            strings.Builder
		inV, inIsT     bool
		inCell         bool
	)
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch e := tok.(type) {
		case xml.StartElement:
			switch e.Name.Local {
			case "row":
				if r, err := strconv.Atoi(attr(e.Attr, "r")); err == nil {
					curRow = r - 1
				} else {
					curRow++
				}
				curCol = -1
			case "c":
				inCell = true
				cType, cRef = attr(e.Attr, "t"), attr(e.Attr, "r")
				val.Reset()
				if r, c, ok := cellRef(cRef); ok {
					curRow, curCol = r, c
				} else {
					curCol++
				}
			case "v":
				inV = true
			case "t":
				inIsT = inCell && cType == "inlineStr"
			case "hyperlink":
				ref := strings.SplitN(attr(e.Attr, "ref"), ":", 2)[0]
				r, c, ok := cellRef(ref)
				if !ok {
					continue
				}
				target := ""
				if id := relID(e.Attr); id != "" && rels != nil {
					target = rels[id]
				}
				if target == "" {
					target = attr(e.Attr, "location")
				}
				if target != "" {
					t.Links[[2]int{r, c}] = target
				}
			}
		case xml.EndElement:
			switch e.Name.Local {
			case "v":
				inV = false
			case "t":
				inIsT = false
			case "c":
				inCell = false
				v := val.String()
				switch cType {
				case "s":
					if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && i >= 0 && i < len(shared) {
						v = shared[i]
					}
				case "b":
					if v == "1" {
						v = "VRAI"
					} else {
						v = "FAUX"
					}
				case "", "n":
					v = tidyNumber(v)
				}
				if v != "" && curRow >= 0 && curCol >= 0 {
					cells[[2]int{curRow, curCol}] = v
					if curRow > maxRow {
						maxRow = curRow
					}
					if curCol > maxCol {
						maxCol = curCol
					}
				}
			}
		case xml.CharData:
			if inV || inIsT {
				val.Write(e)
			}
		}
	}
	t.Rows = make([][]string, maxRow+1)
	for r := range t.Rows {
		t.Rows[r] = make([]string, maxCol+1)
	}
	for k, v := range cells {
		t.Rows[k[0]][k[1]] = v
	}
	return t
}

// tidyNumber : "6" reste "6", "1600.0000000002" devient "1600".
func tidyNumber(v string) string {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return v
	}
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	r := strconv.FormatFloat(f, 'f', 6, 64)
	return strings.TrimRight(strings.TrimRight(r, "0"), ".")
}
