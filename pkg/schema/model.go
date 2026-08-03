package schema

type Database struct {
	Schemas []Schema
}

type Schema struct {
	Name   string
	Tables []Table
}

type Table struct {
	Schema      string
	Name        string
	Comment     string
	Columns     []Column
	PrimaryKey  *PrimaryKey
	ForeignKeys []ForeignKey
	Uniques     []UniqueConstraint
	Checks      []CheckConstraint
	Exclusions  []ExclusionConstraint
	Indexes     []Index
	Example     *Example
}

// Example contains sampled rows for a table. Values are database values and
// are rendered by the TOON encoder.
type Example struct {
	Columns []string
	Rows    [][]any
}

type Column struct {
	Name       string
	NativeType string
	Nullable   bool
	Default    string
	Comment    string
	Identity   string
	Generated  string
}

type PrimaryKey struct {
	Name    string
	Columns []string
}

type ForeignKey struct {
	Name              string
	LocalColumns      []string
	ReferencedSchema  string
	ReferencedTable   string
	ReferencedColumns []string
	OnUpdate          string
	OnDelete          string
}

type UniqueConstraint struct {
	Name    string
	Columns []string
}

type CheckConstraint struct {
	Name       string
	Expression string
}

// ExclusionConstraint is a database constraint that prevents rows whose
// indexed expressions compare equal according to the named operators.
type ExclusionConstraint struct {
	Name       string
	Definition string
}

type Index struct {
	Name             string
	Definition       string
	Unique           bool
	ConstraintBacked bool
	Method           string
	Keys             []string
	IncludedColumns  []string
	Predicate        string
}
