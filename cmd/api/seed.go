package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/categories"
	"github.com/NoeOnDev/go-api-base/internal/civil"
	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/products"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// The accounts of the seed. Both get the same password.
const (
	seedAdminEmail = "admin@example.com"
	seedUserEmail  = "user@example.com"
)

// A seeder fills part of the development database and says what it did, in a few words. It must be
// safe to run again: one whose data is already there does nothing. Each resource adds its own to
// seeders, like its routes in server/routes.go; an example that is deleted takes its seeder along.
type seeder struct {
	name string
	run  func(ctx context.Context, s seedDeps) (string, error)
}

// seedDeps is what the seeders work with.
type seedDeps struct {
	pool     *pgxpool.Pool
	q        *store.Queries
	auth     *auth.Service
	cfg      config.Config
	password string
}

// seeders run in this order: the users first, because the others give them their data.
var seeders = []seeder{
	{"users", seedUsers},
	{"catalog", seedCatalog},
	{"notes", seedNotes},
	{"branches", seedBranches},
}

// runSeed fills the development database with accounts and example data (make seed): the
// equivalent of Laravel's db:seed. It is what a frontend needs to start, and what brings back the
// accounts after the database is recreated.
//
// The password of the accounts is taken from SEED_PASSWORD or asked for in the terminal. It refuses
// to run in production: its accounts have a password everyone on the project knows.
func runSeed() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	if cfg.IsProduction() {
		return errors.New("seed is for development: it does not run with APP_ENV=production")
	}
	password, err := readPassword("SEED_PASSWORD")
	if err != nil {
		return err
	}

	ctx := context.Background()
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool, slog.New(slog.DiscardHandler)); err != nil {
		return err
	}
	return seed(ctx, pool, cfg, password, os.Stdout)
}

// seed runs every seeder and writes what each one did to out.
func seed(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, password string, out io.Writer) error {
	deps := seedDeps{
		pool: pool, q: store.New(pool), auth: auth.NewService(pool, auth.Options{}), cfg: cfg, password: password,
	}
	for _, s := range seeders {
		done, err := s.run(ctx, deps)
		if err != nil {
			return fmt.Errorf("seed %s: %w", s.name, describe(err))
		}
		fmt.Fprintf(out, "%-10s %s\n", s.name, done)
	}
	return nil
}

// seedUsers creates an administrator and a user, with their email verified so they work with
// REQUIRE_EMAIL_VERIFICATION too. An account that already exists is left as it is, password
// included.
func seedUsers(ctx context.Context, s seedDeps) (string, error) {
	var created []string
	for _, u := range []struct{ name, email, role string }{
		{"Admin", seedAdminEmail, auth.RoleAdmin},
		{"User", seedUserEmail, auth.RoleUser},
	} {
		user, err := s.auth.CreateUser(ctx, auth.RegisterInput{Name: u.name, Email: u.email, Password: s.password}, u.role)
		var apiErr *httpx.Error
		if errors.As(err, &apiErr) && apiErr.Code == "email_taken" {
			continue
		}
		if err != nil {
			return "", err
		}
		if err := s.q.MarkEmailVerified(ctx, user.ID); err != nil {
			return "", fmt.Errorf("verify %s: %w", u.email, err)
		}
		created = append(created, u.email)
	}
	if len(created) == 0 {
		return "already there", nil
	}
	return strings.Join(created, " and ") + ", with the seed password", nil
}

// The account and the role of the catalog example.
const (
	seedManagerEmail = "manager@example.com"
	seedManagerRole  = "catalog_manager"
)

// seedCatalog creates a few categories with their products (the categories and products examples),
// one of them inactive: only whoever manages the catalog sees it. It also creates a role with the
// two permissions of the catalog and an account that has it.
func seedCatalog(ctx context.Context, s seedDeps) (string, error) {
	if n, err := s.q.CountCategories(ctx); err != nil || n > 0 {
		return "already there", err
	}
	type product struct {
		name, slug string
		priceCents int64
		stock      int32
		inactive   bool
	}
	catalog := []struct {
		name, slug string
		products   []product
	}{
		{"Coffee", "coffee", []product{
			{name: "Espresso", slug: "espresso", priceCents: 3500, stock: 100},
			{name: "Cappuccino", slug: "cappuccino", priceCents: 5200, stock: 80},
			{name: "Cold brew", slug: "cold-brew", priceCents: 5800, stock: 40},
		}},
		{"Tea", "tea", []product{
			{name: "Green tea", slug: "green-tea", priceCents: 4000, stock: 60},
			{name: "Chai latte", slug: "chai-latte", priceCents: 5500, stock: 0},
		}},
		{"Bakery", "bakery", []product{
			{name: "Croissant", slug: "croissant", priceCents: 3200, stock: 25},
			{name: "Banana bread", slug: "banana-bread", priceCents: 4500, stock: 12},
			{name: "Seasonal cake", slug: "seasonal-cake", priceCents: 6900, stock: 0, inactive: true},
		}},
	}
	created := 0
	for _, c := range catalog {
		category, err := s.q.CreateCategory(ctx, store.CreateCategoryParams{Name: c.name, Slug: c.slug})
		if err != nil {
			return "", fmt.Errorf("create category %s: %w", c.slug, err)
		}
		for _, p := range c.products {
			_, err := s.q.CreateProduct(ctx, store.CreateProductParams{
				CategoryID: category.ID, Name: p.name, Slug: p.slug, PriceCents: p.priceCents, Stock: p.stock, Active: !p.inactive,
			})
			if err != nil {
				return "", fmt.Errorf("create product %s: %w", p.slug, err)
			}
			created++
		}
	}

	// A role of the project and an account with it, to see what a permission does: this account
	// manages the catalog, and nothing else of an administrator.
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.CreateRole(ctx, store.CreateRoleParams{Key: seedManagerRole, Name: "Catalog manager"}); err != nil {
			return err
		}
		return q.AddRolePermissions(ctx, store.AddRolePermissionsParams{
			Role: seedManagerRole, Permissions: []string{categories.PermManage, products.PermManage},
		})
	})
	if err != nil {
		return "", fmt.Errorf("create the role %s: %w", seedManagerRole, err)
	}
	manager, err := s.auth.CreateUser(ctx, auth.RegisterInput{Name: "Manager", Email: seedManagerEmail, Password: s.password}, seedManagerRole)
	if err != nil {
		return "", err
	}
	if err := s.q.MarkEmailVerified(ctx, manager.ID); err != nil {
		return "", fmt.Errorf("verify %s: %w", seedManagerEmail, err)
	}
	return fmt.Sprintf("%d categories and %d products, and %s with the role %s", len(catalog), created, seedManagerEmail, seedManagerRole), nil
}

// seedNotes gives the user of the seed a few notes (the notes example).
func seedNotes(ctx context.Context, s seedDeps) (string, error) {
	user, err := s.q.GetUserByEmail(ctx, seedUserEmail)
	if err != nil {
		return "", fmt.Errorf("find %s: %w", seedUserEmail, err)
	}
	if n, err := s.q.CountNotes(ctx, user.ID); err != nil || n > 0 {
		return "already there", err
	}
	notes := []store.CreateNoteParams{
		{UserID: user.ID, Title: "Welcome", Body: "This note came with the seed. Edit it or delete it."},
		{UserID: user.ID, Title: "Shopping list", Body: "Coffee, milk, bread."},
		{UserID: user.ID, Title: "Ideas"},
	}
	for _, n := range notes {
		if _, err := s.q.CreateNote(ctx, n); err != nil {
			return "", fmt.Errorf("create note %q: %w", n.Title, err)
		}
	}
	return fmt.Sprintf("%d notes of %s", len(notes), seedUserEmail), nil
}

// seedBranches creates a branch in the application's time zone, open from Monday to Saturday (the
// branches example), so the availability and the appointments have something to work with.
func seedBranches(ctx context.Context, s seedDeps) (string, error) {
	if n, err := s.q.CountBranches(ctx); err != nil || n > 0 {
		return "already there", err
	}
	branch, err := s.q.CreateBranch(ctx, store.CreateBranchParams{Name: "Downtown", TimeZone: s.cfg.TimeZone.String()})
	if err != nil {
		return "", fmt.Errorf("create branch: %w", err)
	}
	for weekday := int16(1); weekday <= 6; weekday++ { // 0 is Sunday: closed
		closes := civil.NewTime(18, 0)
		if weekday == 6 {
			closes = civil.NewTime(14, 0)
		}
		err := s.q.CreateBranchHours(ctx, store.CreateBranchHoursParams{
			BranchID: branch.ID, Weekday: weekday, OpensAt: civil.NewTime(9, 0), ClosesAt: closes,
		})
		if err != nil {
			return "", fmt.Errorf("create the hours of the branch: %w", err)
		}
	}
	return fmt.Sprintf("1 branch (%s, in %s)", branch.Name, branch.TimeZone), nil
}
