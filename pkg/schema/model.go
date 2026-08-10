package schema

type Database struct {
	Dialect    string
	Name       string
	Extensions []Extension
	Schemas    []Schema
}

// Extension is a database-installed extension or plugin.
type Extension struct {
	Name    string
	Version string
	Schema  string
}

type Schema struct {
	Name      string
	Enums     []Enum
	Types     []UserDefinedType
	Sequences []Sequence
	Synonyms  []Synonym
	Objects   []Object
	Routines  []Routine
	Tables    []Table
}

// Enum is a schema-qualified enumerated type with values in database order.
type Enum struct {
	Name   string
	Values []string
}

// UserDefinedType describes an alias or table type declared by a database.
type UserDefinedType struct {
	Name       string
	Kind       string
	NativeType string
}

// Sequence describes a database-managed numeric sequence.
type Sequence struct {
	Name       string
	NativeType string
	Start      string
	Increment  string
	Minimum    string
	Maximum    string
	Cyclic     bool
}

// Synonym is an alternate name for another database object.
type Synonym struct {
	Name   string
	Target string
}

// Object is a vendor-neutral schema object that does not map to a table,
// routine, type, sequence, or synonym.
type Object struct {
	Name       string
	Kind       string
	Definition string
	Properties []Property
}

// Property preserves ordered vendor-specific metadata for an Object.
type Property struct {
	Name  string
	Value string
}

// Routine describes a stored function or procedure.
type Routine struct {
	Name       string
	Kind       string
	Arguments  string
	ReturnType string
	Language   string
	Definition string
}

type Table struct {
	Schema      string
	Name        string
	Kind        string
	Definition  string
	Comment     string
	Columns     []Column
	PrimaryKey  *PrimaryKey
	ForeignKeys []ForeignKey
	Uniques     []UniqueConstraint
	Checks      []CheckConstraint
	Exclusions  []ExclusionConstraint
	Indexes     []Index
	Triggers    []Trigger
	Example     *Example
}

// Trigger describes a trigger attached to a table.
type Trigger struct {
	Name       string
	Timing     string
	Events     []string
	Enabled    bool
	Definition string
}

// Example contains sampled rows for a table. Values are database values and
// are rendered by the TOON encoder.
type Example struct {
	Columns     []string
	ColumnTypes []string
	Rows        [][]any
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
