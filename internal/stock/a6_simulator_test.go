package stock

import (
	"fmt"
	"testing"
	"time"
)

func TestA6SimulatorCommandBoundary(t *testing.T) {
	f := setup(t)
	p := f.product("sim-scope")
	f.receive(p, "SIM", 10)
	f.deliver(p, 5)
	if _, e := f.owner.Exec(f.ctx, `UPDATE "pl-wy-users" SET role='simulator' WHERE id=$1`, f.pharmacist); e != nil {
		t.Fatal(e)
	}
	// The service account can sell in its explicitly assigned branch.
	in := f.order(p, 1)
	f.exec("sale.create", "", in)
	for _, cmd := range []string{"product.create", "transfer.create", "count.start", "price.create", "need.create"} {
		_, e := f.svc.Execute(f.ctx, f.pharmacist, cmd, "", fmt.Sprint(time.Now().UnixNano()), "sim-boundary", Input{LocationID: f.branch})
		assertCode(t, e, "FORBIDDEN")
	}
	for _, resource := range []string{"prescriptions", "controlled-ledger", "sales", "suggestions", "report-finance"} {
		_, e := f.svc.Read(f.ctx, f.pharmacist, resource, f.branch, "", "", 50)
		assertCode(t, e, "FORBIDDEN")
	}
	_, e := f.svc.Execute(f.ctx, f.pharmacist, "sale.create", "", "sim-foreign-key", "sim-boundary", Input{LocationID: "2", Items: []Item{{p, "1", "unit"}}})
	assertCode(t, e, "FORBIDDEN")
	f.invariant()
}
