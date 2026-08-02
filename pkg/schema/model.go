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
	Indexes     []Index
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

type Index struct {
	Name             string
	Definition       string
	Unique           bool
	ConstraintBacked bool
}
