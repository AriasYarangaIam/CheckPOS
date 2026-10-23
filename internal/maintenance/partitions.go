package maintenance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Este archivo implementa el mantenimiento de particiones de `sales` (Sprint 6, W6-DB-2;
// docs/DATABASE.md §6.3-§6.7).
//
// Dos operaciones separadas a propósito (decisión D6 de
// odd/tasks/sprint-6-offline-sync.md):
//
//   - `EnsurePartitions` es el job periódico: pre-crea los meses del horizonte de forma
//     IDEMPOTENTE y no toca `sales_default` — la remediación es una decisión operativa, no
//     un efecto colateral silencioso;
//   - `RemediateDefault` es la remediación F5 y solo corre cuando se la invoca: mueve las
//     filas varadas a su partición mensual en UNA transacción y deja `sales_default` vacía,
//     o revierte completa y deja la alerta en manos de una persona (§6.5).
//
// El horizonte de seis meses no es un parámetro de ajuste: es una constante de diseño
// (§6.3). Un mes de particiones siempre existe incluso si el job falta varias corridas.

// HorizonMonths es el horizonte de pre-creación de §6.3: seis meses hacia adelante.
const HorizonMonths = 6

// partitionLayout es el formato de nombre que las migraciones ya fijaron para las
// particiones mensuales (`sales_2026_10`): el job NO inventa una segunda convención.
const partitionLayout = "2006_01"

// querier es lo que necesitan los helpers: sirve igual un *sql.DB que una *sql.Tx, así la
// misma creación corre suelta o dentro de la transacción de la remediación.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// PartitionName devuelve el nombre de la partición mensual de `month`.
func PartitionName(month time.Time) string {
	return "sales_" + month.UTC().Format(partitionLayout)
}

// monthStart normaliza un instante al primer día de su mes en UTC: las particiones son
// mensuales por calendario, no por ventana móvil.
func monthStart(moment time.Time) time.Time {
	year, month, _ := moment.UTC().Date()
	return time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
}

// monthBounds devuelve el rango semiabierto [desde, hasta) de la partición de `month`.
func monthBounds(month time.Time) (time.Time, time.Time) {
	start := monthStart(month)
	return start, start.AddDate(0, 1, 0)
}

// MonthsToEnsure devuelve el mes corriente y los `horizon` meses siguientes: la lista que
// el job periódico tiene que dejar creada (§6.3).
func MonthsToEnsure(now time.Time, horizon int) []time.Time {
	start := monthStart(now)
	months := make([]time.Time, 0, horizon+1)
	for offset := 0; offset <= horizon; offset++ {
		months = append(months, start.AddDate(0, offset, 0))
	}
	return months
}

// EnsurePartitions crea, de forma idempotente, `sales_default` y la partición de cada mes
// del horizonte. Correrla dos veces el mismo día deja el mismo conjunto: cada creación está
// guardada por `IF NOT EXISTS` (§6.3). Devuelve los nombres de las particiones mensuales del
// horizonte, para que la salida del job sea verificable.
func EnsurePartitions(ctx context.Context, q querier, now time.Time) ([]string, error) {
	if q == nil {
		return nil, errors.New("the partition job needs a database handle")
	}

	// La partición DEFAULT es la red de seguridad: existe desde la primera migración y se
	// recrea acá por si una remediación anterior quedó a medias.
	if err := createMonthlyPartition(ctx, q, salesDefaultPartition, nil); err != nil {
		return nil, err
	}

	months := MonthsToEnsure(now, HorizonMonths)
	names := make([]string, 0, len(months))
	for _, month := range months {
		name := PartitionName(month)
		if err := createMonthlyPartition(ctx, q, name, monthPointer(month)); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, nil
}

// salesDefaultPartition es el nombre que §6.4 fija para la partición DEFAULT.
const salesDefaultPartition = "sales_default"

// tenantPredicate es el predicado de tenant de §5.3, escrito tal cual: la partición nueva
// recibe las MISMAS políticas que sus hermanas, no una variante.
const tenantPredicate = `tenant_id = current_setting('app.current_tenant_id')::uuid`

// createMonthlyPartition crea la partición y le espeja el aislamiento. Con `month` nulo crea
// la DEFAULT; con un mes, la partición mensual con su rango de calendario.
//
// El espejo NO es opcional ni cosmético: PostgreSQL no propaga concesiones ni políticas de la
// tabla particionada a sus particiones (medido y documentado en
// migrations/20261016000002_tenant_rls.sql), así que una partición creada sin este paso deja
// el mes siguiente sin concesiones —falla cerrado— o con concesiones y sin políticas —fuga
// entre tenants—. Por eso el espejo viaja en la MISMA creación.
func createMonthlyPartition(ctx context.Context, q querier, name string, month *time.Time) error {
	exists, err := partitionExists(ctx, q, name)
	if err != nil {
		return err
	}
	if exists {
		// Idempotencia real: la partición ya está (la creó una corrida anterior o la
		// migración) y ya tiene su espejo; el job no la vuelve a tocar.
		return nil
	}

	var statement string
	if month == nil {
		statement = fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s PARTITION OF sales DEFAULT", name)
	} else {
		from, to := monthBounds(*month)
		statement = fmt.Sprintf(
			"CREATE TABLE IF NOT EXISTS %s PARTITION OF sales FOR VALUES FROM ('%s') TO ('%s')",
			name, from.Format(time.DateOnly), to.Format(time.DateOnly))
	}
	if _, err := q.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("create the partition %s: %w", name, err)
	}

	return mirrorPartitionIsolation(ctx, q, name)
}

// mirrorPartitionIsolation repite, sobre la partición nueva, el pin de RLS, las cuatro
// políticas de tenant y las concesiones de `pos_app` que la migración aplicó a las
// existentes: el acceso directo a la partición se comporta igual que el acceso por el padre.
func mirrorPartitionIsolation(ctx context.Context, q querier, name string) error {
	statements := []string{
		fmt.Sprintf("ALTER TABLE %s ENABLE ROW LEVEL SECURITY", name),
		fmt.Sprintf("ALTER TABLE %s FORCE ROW LEVEL SECURITY", name),
		fmt.Sprintf("GRANT SELECT, INSERT ON TABLE %s TO pos_app", name),
		fmt.Sprintf("CREATE POLICY %[1]s_tenant_select ON %[1]s FOR SELECT USING (%[2]s)", name, tenantPredicate),
		fmt.Sprintf("CREATE POLICY %[1]s_tenant_insert ON %[1]s FOR INSERT WITH CHECK (%[2]s)", name, tenantPredicate),
		fmt.Sprintf("CREATE POLICY %[1]s_tenant_update ON %[1]s FOR UPDATE USING (%[2]s) WITH CHECK (%[2]s)", name, tenantPredicate),
		fmt.Sprintf("CREATE POLICY %[1]s_tenant_delete ON %[1]s FOR DELETE USING (%[2]s)", name, tenantPredicate),
	}

	for _, statement := range statements {
		if _, err := q.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("mirror the isolation of the partition %s: %w", name, err)
		}
	}
	return nil
}

// monthPointer devuelve el puntero a una copia del mes, para distinguir «mes» de «DEFAULT»
// sin un segundo parámetro booleano.
func monthPointer(month time.Time) *time.Time {
	copied := month
	return &copied
}

// DefaultPartitionRows cuenta las filas varadas en `sales_default`: es LA señal de salud del
// subsistema de particiones (§6.4, §6.7). Una DEFAULT vacía es el estado estable.
func DefaultPartitionRows(ctx context.Context, q querier) (int64, error) {
	if q == nil {
		return 0, errors.New("the sales_default check needs a database handle")
	}

	var rows int64
	if err := q.QueryRowContext(ctx, "SELECT count(*) FROM "+salesDefaultPartition).Scan(&rows); err != nil {
		return 0, fmt.Errorf("count the rows stranded in %s: %w", salesDefaultPartition, err)
	}
	return rows, nil
}

// RemediationResult es la evidencia de una remediación F5.
type RemediationResult struct {
	// RowsMoved es el número de filas que la DEFAULT tenía cuando la remediación empezó.
	RowsMoved int64
	// TargetPartition es la partición mensual a la que se re-adjuntó la DEFAULT, vacía
	// cuando no había nada que mover.
	TargetPartition string
}

// RemediateDefault aplica F5 (§6.5) en UNA transacción: separa la DEFAULT, la renombra a la
// partición mensual de sus filas, la re-adjunta con el rango de ese mes y crea una DEFAULT
// nueva y vacía. Al final, o `sales_default` está vacía o la transacción revirtió completa.
//
// Límite declarado: si las filas varadas cubren MÁS de un mes, no existe un rango único al
// que re-adjuntar y la remediación revierte con un error explícito — es el caso que §6.5
// manda a intervención humana, no un caso que este job pueda inventar.
func RemediateDefault(ctx context.Context, db *sql.DB, now time.Time) (RemediationResult, error) {
	if db == nil {
		return RemediationResult{}, errors.New("the remediation needs a database handle")
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return RemediationResult{}, fmt.Errorf("open the remediation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stranded, err := DefaultPartitionRows(ctx, tx)
	if err != nil {
		return RemediationResult{}, err
	}

	// Sin filas varadas no hay nada que remediar: el horizonte se asegura igual, porque la
	// corrida es también la oportunidad de ponerse al día (§6.6).
	if stranded == 0 {
		if _, err := EnsurePartitions(ctx, tx, now); err != nil {
			return RemediationResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return RemediationResult{}, fmt.Errorf("commit the remediation transaction: %w", err)
		}
		return RemediationResult{}, nil
	}

	month, err := singleStrandedMonth(ctx, tx)
	if err != nil {
		return RemediationResult{}, err
	}

	target := PartitionName(month)
	if exists, err := partitionExists(ctx, tx, target); err != nil {
		return RemediationResult{}, err
	} else if exists {
		// La partición del mes ya existe: las filas varadas no pueden re-adjuntarse ahí sin
		// solaparse con ella. Es intervención humana, no un reintento.
		return RemediationResult{}, fmt.Errorf(
			"the stranded rows belong to %s but that partition already exists: human intervention required", target)
	}

	if _, err := tx.ExecContext(ctx, "ALTER TABLE sales DETACH PARTITION "+salesDefaultPartition); err != nil {
		return RemediationResult{}, fmt.Errorf("detach the default partition: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "ALTER TABLE "+salesDefaultPartition+" RENAME TO "+target); err != nil {
		return RemediationResult{}, fmt.Errorf("rename the detached default partition to %s: %w", target, err)
	}

	from, to := monthBounds(month)
	attach := fmt.Sprintf(
		"ALTER TABLE sales ATTACH PARTITION %s FOR VALUES FROM ('%s') TO ('%s')",
		target, from.Format(time.DateOnly), to.Format(time.DateOnly))
	if _, err := tx.ExecContext(ctx, attach); err != nil {
		return RemediationResult{}, fmt.Errorf("attach the detached partition as %s: %w", target, err)
	}

	if err := createMonthlyPartition(ctx, tx, salesDefaultPartition, nil); err != nil {
		return RemediationResult{}, err
	}
	if _, err := EnsurePartitions(ctx, tx, now); err != nil {
		return RemediationResult{}, err
	}

	// La garantía del contrato se comprueba ANTES del COMMIT: si la DEFAULT no quedó vacía,
	// la transacción revierte entera y la alerta sigue en pie.
	remaining, err := DefaultPartitionRows(ctx, tx)
	if err != nil {
		return RemediationResult{}, err
	}
	if remaining != 0 {
		return RemediationResult{}, fmt.Errorf(
			"the remediation left %d rows in %s: revoking the whole transaction", remaining, salesDefaultPartition)
	}

	if err := tx.Commit(); err != nil {
		return RemediationResult{}, fmt.Errorf("commit the remediation transaction: %w", err)
	}
	return RemediationResult{RowsMoved: stranded, TargetPartition: target}, nil
}

// singleStrandedMonth devuelve el mes al que pertenecen TODAS las filas varadas, o el error
// que manda el caso a intervención humana cuando cubren más de un mes.
func singleStrandedMonth(ctx context.Context, q querier) (time.Time, error) {
	var (
		first sql.NullTime
		last  sql.NullTime
	)
	if err := q.QueryRowContext(ctx,
		"SELECT min(sold_on), max(sold_on) FROM "+salesDefaultPartition).Scan(&first, &last); err != nil {
		return time.Time{}, fmt.Errorf("read the stranded date range: %w", err)
	}
	if !first.Valid || !last.Valid {
		return time.Time{}, errors.New("the default partition reported rows but no date range")
	}

	from := monthStart(first.Time)
	if !from.Equal(monthStart(last.Time)) {
		return time.Time{}, fmt.Errorf(
			"the stranded rows span more than one month (%s..%s): human intervention required",
			first.Time.Format(time.DateOnly), last.Time.Format(time.DateOnly))
	}
	return from, nil
}

// partitionExists dice si la tabla de la partición ya está en el esquema.
func partitionExists(ctx context.Context, q querier, name string) (bool, error) {
	var exists bool
	if err := q.QueryRowContext(ctx,
		"SELECT to_regclass('public.' || $1) IS NOT NULL", name).Scan(&exists); err != nil {
		return false, fmt.Errorf("look up the partition %s: %w", name, err)
	}
	return exists, nil
}
