package rel

import (
	"fmt"
)

// Map can be used as modification for repository insert or update operation.
// This allows inserting or updating only on specified field.
// Insert/Update of has one or belongs to can be done using other Map as a value.
// Insert/Update of has many can be done using slice of Map as a value.
// Map is intended to be used internally within application, and not to be exposed directly as an APIs.
type Map map[string]interface{}

// Apply modification.
func (m Map) Apply(doc *Document, modification *Modification) {
	var (
		pField = doc.PrimaryField()
		pValue = doc.PrimaryValue()
	)

	for field, value := range m {
		switch v := value.(type) {
		case Map:
			var (
				assoc = doc.Association(field)
			)

			if assoc.Type() != HasOne && assoc.Type() != BelongsTo {
				panic(fmt.Sprint("rel: cannot associate has many", v, "as", field, "into", doc.Table()))
			}

			var (
				assocDoc, _       = assoc.Document()
				assocModification = Apply(assocDoc, v)
			)

			modification.SetAssoc(field, assocModification)
		case []Map:
			var (
				assoc            = doc.Association(field)
				mods, deletedIDs = applyMaps(v, assoc)
			)

			modification.SetAssoc(field, mods...)
			modification.SetDeletedIDs(field, deletedIDs)
		default:
			if field == pField {
				if v != pValue {
					panic(fmt.Sprint("rel: replacing primary value (", pValue, " become ", v, ") is not allowed"))
				} else {
					continue
				}
			}

			if !doc.SetValue(field, v) {
				panic(fmt.Sprint("rel: cannot assign ", v, " as ", field, " into ", doc.Table()))
			}

			modification.Add(Set(field, v))
		}
	}
}

func applyMaps(maps []Map, assoc Association) ([]Modification, []interface{}) {
	var (
		deletedIDs []interface{}
		mods       = make([]Modification, len(maps))
		col, _     = assoc.Collection()
		pField     = col.PrimaryField()
		pIndex     = make(map[interface{}]int)
		pValues    = col.PrimaryValue().([]interface{})
	)

	for i, v := range pValues {
		pIndex[v] = i
	}

	var (
		curr    = 0
		inserts []Map
	)

	for _, m := range maps {
		if pChange, changed := m[pField]; changed {
			// update
			pID, ok := pIndex[pChange]
			if !ok {
				panic("rel: cannot update has many assoc that is not loaded or doesn't belong to this record")
			}

			if pID != curr {
				col.Swap(pID, curr)
				pValues[pID], pValues[curr] = pValues[curr], pValues[pID]
			}

			mods[curr] = Apply(col.Get(curr), m)
			delete(pIndex, pChange)
			curr++
		} else {
			inserts = append(inserts, m)
		}
	}

	// delete stales
	if curr < col.Len() {
		deletedIDs = pValues[curr:]
		col.Truncate(0, curr)
	} else {
		deletedIDs = []interface{}{}
	}

	// inserts remaining
	for i, m := range inserts {
		mods[curr+i] = Apply(col.Add(), m)
	}

	return mods, deletedIDs

}
