package sql

import (
	"strconv"
	"strings"
	"sync"

	"github.com/Fs02/rel"
)

// UnescapeCharacter disable field escaping when it starts with this character.
var UnescapeCharacter byte = '^'

var fieldCache sync.Map

// Builder defines information of query b.
type Builder struct {
	config      *Config
	returnField string
	count       int
}

// Find generates query for select.
func (b *Builder) Find(query rel.Query) (string, []interface{}) {
	var (
		buffer Buffer
	)

	// TODO: calculate arguments size and if possible buffer size

	b.fields(&buffer, query.SelectQuery.OnlyDistinct, query.SelectQuery.Fields)
	b.query(&buffer, query)

	return buffer.String(), buffer.Arguments
}

// Aggregate generates query for aggregation.
func (b *Builder) Aggregate(query rel.Query, mode string, field string) (string, []interface{}) {
	var (
		buffer Buffer
	)

	buffer.WriteString("SELECT ")
	buffer.WriteString(mode)
	buffer.WriteByte('(')
	buffer.WriteString(b.escape(field))
	buffer.WriteString(") AS ")
	buffer.WriteString(mode)

	for _, f := range query.GroupQuery.Fields {
		buffer.WriteByte(',')
		buffer.WriteString(b.escape(f))
	}

	b.query(&buffer, query)

	return buffer.String(), buffer.Arguments
}

func (b *Builder) query(buffer *Buffer, query rel.Query) {
	b.from(buffer, query.Table)
	b.join(buffer, query.JoinQuery)
	b.where(buffer, query.WhereQuery)

	if len(query.GroupQuery.Fields) > 0 {
		b.groupBy(buffer, query.GroupQuery.Fields)
		b.having(buffer, query.GroupQuery.Filter)
	}

	b.orderBy(buffer, query.SortQuery)
	b.limitOffset(buffer, query.LimitQuery, query.OffsetQuery)

	if query.LockQuery != "" {
		buffer.WriteByte(' ')
		buffer.WriteString(string(query.LockQuery))
	}

	buffer.WriteString(";")
}

// Insert generates query for insert.
func (b *Builder) Insert(table string, modifies map[string]rel.Modify) (string, []interface{}) {
	var (
		buffer Buffer
		count  = len(modifies)
	)

	buffer.WriteString("INSERT INTO ")
	buffer.WriteString(b.escape(table))

	if count == 0 && b.config.InsertDefaultValues {
		buffer.WriteString(" DEFAULT VALUES")
	} else {
		buffer.Arguments = make([]interface{}, count)
		buffer.WriteString(" (")

		i := 0
		for field, mod := range modifies {
			if mod.Type == rel.ChangeSetOp {
				buffer.WriteString(b.config.EscapeChar)
				buffer.WriteString(field)
				buffer.WriteString(b.config.EscapeChar)
				buffer.Arguments[i] = mod.Value
			}

			if i < count-1 {
				buffer.WriteByte(',')
			}
			i++
		}

		buffer.WriteString(") VALUES ")

		buffer.WriteByte('(')
		for i := 0; i < len(buffer.Arguments); i++ {
			buffer.WriteString(b.ph())

			if i < len(buffer.Arguments)-1 {
				buffer.WriteByte(',')
			}
		}
		buffer.WriteByte(')')
	}

	if b.returnField != "" {
		buffer.WriteString(" RETURNING ")
		buffer.WriteString(b.config.EscapeChar)
		buffer.WriteString(b.returnField)
		buffer.WriteString(b.config.EscapeChar)
	}

	buffer.WriteString(";")

	return buffer.String(), buffer.Arguments
}

// InsertAll generates query for multiple insert.
func (b *Builder) InsertAll(table string, fields []string, bulkModifies []map[string]rel.Modify) (string, []interface{}) {
	var (
		buffer        Buffer
		fieldsCount   = len(fields)
		modifiesCount = len(bulkModifies)
	)

	buffer.Arguments = make([]interface{}, 0, fieldsCount*modifiesCount)

	buffer.WriteString("INSERT INTO ")

	buffer.WriteString(b.config.EscapeChar)
	buffer.WriteString(table)
	buffer.WriteString(b.config.EscapeChar)
	buffer.WriteString(" (")

	for i := range fields {
		buffer.WriteString(b.config.EscapeChar)
		buffer.WriteString(fields[i])
		buffer.WriteString(b.config.EscapeChar)

		if i < fieldsCount-1 {
			buffer.WriteByte(',')
		}
	}

	buffer.WriteString(") VALUES ")

	for i, modifies := range bulkModifies {
		buffer.WriteByte('(')

		for j, field := range fields {
			if mod, ok := modifies[field]; ok && mod.Type == rel.ChangeSetOp {
				buffer.WriteString(b.ph())
				buffer.Append(mod.Value)
			} else {
				buffer.WriteString("DEFAULT")
			}

			if j < fieldsCount-1 {
				buffer.WriteByte(',')
			}
		}

		if i < modifiesCount-1 {
			buffer.WriteString("),")
		} else {
			buffer.WriteByte(')')
		}
	}

	if b.returnField != "" {
		buffer.WriteString(" RETURNING ")
		buffer.WriteString(b.config.EscapeChar)
		buffer.WriteString(b.returnField)
		buffer.WriteString(b.config.EscapeChar)
	}

	buffer.WriteString(";")

	return buffer.String(), buffer.Arguments
}

// Update generates query for update.
func (b *Builder) Update(table string, modifies map[string]rel.Modify, filter rel.FilterQuery) (string, []interface{}) {
	var (
		buffer Buffer
		count  = len(modifies)
	)

	buffer.WriteString("UPDATE ")
	buffer.WriteString(b.config.EscapeChar)
	buffer.WriteString(table)
	buffer.WriteString(b.config.EscapeChar)
	buffer.WriteString(" SET ")

	i := 0
	for field, mod := range modifies {
		switch mod.Type {
		case rel.ChangeSetOp:
			buffer.WriteString(b.escape(field))
			buffer.WriteByte('=')
			buffer.WriteString(b.ph())
			buffer.Append(mod.Value)
		case rel.ChangeIncOp:
			buffer.WriteString(b.escape(field))
			buffer.WriteByte('=')
			buffer.WriteString(b.escape(field))
			buffer.WriteByte('+')
			buffer.WriteString(b.ph())
			buffer.Append(mod.Value)
		case rel.ChangeFragmentOp:
			buffer.WriteString(field)
			buffer.Append(mod.Value.([]interface{})...)
		}

		if i < count-1 {
			buffer.WriteByte(',')
		}
		i++
	}

	b.where(&buffer, filter)

	buffer.WriteString(";")

	return buffer.String(), buffer.Arguments
}

// Delete generates query for delete.
func (b *Builder) Delete(table string, filter rel.FilterQuery) (string, []interface{}) {
	var (
		buffer Buffer
	)

	buffer.WriteString("DELETE FROM ")
	buffer.WriteString(b.config.EscapeChar)
	buffer.WriteString(table)
	buffer.WriteString(b.config.EscapeChar)

	b.where(&buffer, filter)

	buffer.WriteString(";")

	return buffer.String(), buffer.Arguments
}

func (b *Builder) fields(buffer *Buffer, distinct bool, fields []string) {
	if len(fields) == 0 {
		if distinct {
			buffer.WriteString("SELECT DISTINCT *")
			return
		}
		buffer.WriteString("SELECT *")
		return
	}

	buffer.WriteString("SELECT ")

	if distinct {
		buffer.WriteString("DISTINCT ")
	}

	l := len(fields) - 1
	for i, f := range fields {
		buffer.WriteString(b.escape(f))

		if i < l {
			buffer.WriteByte(',')
		}
	}
}

func (b *Builder) from(buffer *Buffer, table string) {
	buffer.WriteString(" FROM ")
	buffer.WriteString(b.config.EscapeChar)
	buffer.WriteString(table)
	buffer.WriteString(b.config.EscapeChar)
}

func (b *Builder) join(buffer *Buffer, joins []rel.JoinQuery) {
	if len(joins) == 0 {
		return
	}

	for _, join := range joins {
		buffer.WriteByte(' ')
		buffer.WriteString(join.Mode)
		buffer.WriteByte(' ')

		if join.Table != "" {
			buffer.WriteString(b.config.EscapeChar)
			buffer.WriteString(join.Table)
			buffer.WriteString(b.config.EscapeChar)
			buffer.WriteString(" ON ")
			buffer.WriteString(b.escape(join.From))
			buffer.WriteString("=")
			buffer.WriteString(b.escape(join.To))
		}

		buffer.Append(join.Arguments...)
	}
}

func (b *Builder) where(buffer *Buffer, filter rel.FilterQuery) {
	if filter.None() {
		return
	}

	buffer.WriteString(" WHERE ")
	b.filter(buffer, filter)
}

func (b *Builder) groupBy(buffer *Buffer, fields []string) {
	buffer.WriteString(" GROUP BY ")

	l := len(fields) - 1
	for i, f := range fields {
		buffer.WriteString(b.escape(f))

		if i < l {
			buffer.WriteByte(',')
		}
	}
}

func (b *Builder) having(buffer *Buffer, filter rel.FilterQuery) {
	if filter.None() {
		return
	}

	buffer.WriteString(" HAVING ")
	b.filter(buffer, filter)
}

func (b *Builder) orderBy(buffer *Buffer, orders []rel.SortQuery) {
	var (
		length = len(orders)
	)

	if length == 0 {
		return
	}

	buffer.WriteString(" ORDER BY")
	for i, order := range orders {
		buffer.WriteByte(' ')
		buffer.WriteString(b.escape(order.Field))

		if order.Asc() {
			buffer.WriteString(" ASC")
		} else {
			buffer.WriteString(" DESC")
		}

		if i < length-1 {
			buffer.WriteByte(',')
		}
	}
}

func (b *Builder) limitOffset(buffer *Buffer, limit rel.Limit, offset rel.Offset) {
	if limit > 0 {
		buffer.WriteString(" LIMIT ")
		buffer.WriteString(strconv.Itoa(int(limit)))

		if offset > 0 {
			buffer.WriteString(" OFFSET ")
			buffer.WriteString(strconv.Itoa(int(offset)))
		}
	}
}

func (b *Builder) filter(buffer *Buffer, filter rel.FilterQuery) {
	switch filter.Type {
	case rel.FilterAndOp:
		b.build(buffer, "AND", filter.Inner)
	case rel.FilterOrOp:
		b.build(buffer, "OR", filter.Inner)
	case rel.FilterNotOp:
		buffer.WriteString("NOT ")
		b.build(buffer, "AND", filter.Inner)
	case rel.FilterEqOp,
		rel.FilterNeOp,
		rel.FilterLtOp,
		rel.FilterLteOp,
		rel.FilterGtOp,
		rel.FilterGteOp:
		b.buildComparison(buffer, filter)
	case rel.FilterNilOp:
		buffer.WriteString(b.escape(filter.Field))
		buffer.WriteString(" IS NULL")
	case rel.FilterNotNilOp:
		buffer.WriteString(b.escape(filter.Field))
		buffer.WriteString(" IS NOT NULL")
	case rel.FilterInOp,
		rel.FilterNinOp:
		b.buildInclusion(buffer, filter)
	case rel.FilterLikeOp:
		buffer.WriteString(b.escape(filter.Field))
		buffer.WriteString(" LIKE ")
		buffer.WriteString(b.ph())
		buffer.Append(filter.Value)
	case rel.FilterNotLikeOp:
		buffer.WriteString(b.escape(filter.Field))
		buffer.WriteString(" NOT LIKE ")
		buffer.WriteString(b.ph())
		buffer.Append(filter.Value)
	case rel.FilterFragmentOp:
		buffer.WriteString(filter.Field)
		buffer.Append(filter.Value.([]interface{})...)
	}
}

func (b *Builder) build(buffer *Buffer, op string, inner []rel.FilterQuery) {
	var (
		length = len(inner)
	)

	if length > 1 {
		buffer.WriteByte('(')
	}

	for i, c := range inner {
		b.filter(buffer, c)

		if i < length-1 {
			buffer.WriteByte(' ')
			buffer.WriteString(op)
			buffer.WriteByte(' ')
		}
	}

	if length > 1 {
		buffer.WriteByte(')')
	}
}

func (b *Builder) buildComparison(buffer *Buffer, filter rel.FilterQuery) {
	buffer.WriteString(b.escape(filter.Field))

	switch filter.Type {
	case rel.FilterEqOp:
		buffer.WriteByte('=')
	case rel.FilterNeOp:
		buffer.WriteString("<>")
	case rel.FilterLtOp:
		buffer.WriteByte('<')
	case rel.FilterLteOp:
		buffer.WriteString("<=")
	case rel.FilterGtOp:
		buffer.WriteByte('>')
	case rel.FilterGteOp:
		buffer.WriteString(">=")
	}

	buffer.WriteString(b.ph())
	buffer.Append(filter.Value)
}

func (b *Builder) buildInclusion(buffer *Buffer, filter rel.FilterQuery) {
	var (
		values = filter.Value.([]interface{})
	)

	buffer.WriteString(b.escape(filter.Field))

	if filter.Type == rel.FilterInOp {
		buffer.WriteString(" IN (")
	} else {
		buffer.WriteString(" NOT IN (")
	}

	buffer.WriteString(b.ph())
	for i := 1; i <= len(values)-1; i++ {
		buffer.WriteByte(',')
		buffer.WriteString(b.ph())
	}
	buffer.WriteByte(')')
	buffer.Append(values...)
}

func (b *Builder) ph() string {
	if b.config.Ordinal {
		b.count++
		return b.config.Placeholder + strconv.Itoa(b.count)
	}

	return b.config.Placeholder
}

type fieldCacheKey struct {
	field  string
	escape string
}

func (b *Builder) escape(field string) string {
	if b.config.EscapeChar == "" || field == "*" {
		return field
	}

	key := fieldCacheKey{field: field, escape: b.config.EscapeChar}
	escapedField, ok := fieldCache.Load(key)
	if ok {
		return escapedField.(string)
	}

	if len(field) > 0 && field[0] == UnescapeCharacter {
		escapedField = field[1:]
	} else if start, end := strings.IndexRune(field, '('), strings.IndexRune(field, ')'); start >= 0 && end >= 0 && end > start {
		escapedField = field[:start+1] + b.escape(field[start+1:end]) + field[end:]
	} else if strings.HasSuffix(field, "*") {
		escapedField = b.config.EscapeChar + strings.Replace(field, ".", b.config.EscapeChar+".", 1)
	} else {
		escapedField = b.config.EscapeChar +
			strings.Replace(field, ".", b.config.EscapeChar+"."+b.config.EscapeChar, 1) +
			b.config.EscapeChar
	}

	fieldCache.Store(key, escapedField)
	return escapedField.(string)
}

// Returning append returning to insert rel.
func (b *Builder) Returning(field string) *Builder {
	b.returnField = field
	return b
}

// NewBuilder create new SQL builder.
func NewBuilder(config *Config) *Builder {
	return &Builder{
		config: config,
	}
}
