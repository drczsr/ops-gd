package executor

import (
	"fmt"
	"strconv"

	"gongdan/internal/model"
)

// ALBConfig holds ALB create/delete settings.
type ALBConfig struct {
	Enabled bool

	// SDK settings
	Region                string
	ResourceGroupID       string
	VpcID                 string
	HealthCheckTemplateID string

	LoginLoadBalancerID string
	LoginListenerID     string
	PayLoadBalancerID   string
	PayListenerID       string
	WSLoadBalancerID    string
	WSListenerID        string
}

type albSetup interface {
	SetupForRows(rows []model.NewServerRow, log LogFunc) error
}

type albTeardown interface {
	TeardownForRow(p model.DeleteServerParams, log LogFunc) error
}

// ALBScripts is ALB SDK setup/teardown entry.
type ALBScripts struct {
	setup       albSetup
	setupErr    error
	teardown    albTeardown
	teardownErr error
}

func NewALBScripts(cfg *ALBConfig) *ALBScripts {
	sdk, err := NewALBSDKTeardown(cfg)
	return &ALBScripts{
		setup:       sdk,
		setupErr:    err,
		teardown:    sdk,
		teardownErr: err,
	}
}

func (a *ALBScripts) SetupForRows(rows []model.NewServerRow, log LogFunc) error {
	if a.setupErr != nil {
		return fmt.Errorf("init ALB setup failed: %w", a.setupErr)
	}
	if a.setup == nil {
		return fmt.Errorf("ALB setup not initialized")
	}
	return a.setup.SetupForRows(rows, log)
}

func (a *ALBScripts) TeardownForRow(p model.DeleteServerParams, log LogFunc) error {
	if a.teardownErr != nil {
		return fmt.Errorf("init ALB teardown failed: %w", a.teardownErr)
	}
	if a.teardown == nil {
		return fmt.Errorf("ALB teardown not initialized")
	}
	return a.teardown.TeardownForRow(p, log)
}

func pairedBattleCopy(rows []model.NewServerRow, battleID int) (model.NewServerRow, bool) {
	bid := strconv.Itoa(battleID)
	for _, r := range rows {
		if r.Kind == "copy" && r.Fields["BattleWorldID"] == bid && r.Fields["GlobalCenterWorldID"] == "-1" {
			return r, true
		}
	}
	return model.NewServerRow{}, false
}
