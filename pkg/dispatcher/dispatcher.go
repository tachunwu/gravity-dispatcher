package dispatcher

import (
	"encoding/json"
	"fmt"

	"github.com/BrobridgeOrg/gravity-dispatcher/pkg/configs"
	"github.com/BrobridgeOrg/gravity-dispatcher/pkg/connector"
	"github.com/BrobridgeOrg/gravity-dispatcher/pkg/dispatcher/config_store"
	"github.com/BrobridgeOrg/gravity-dispatcher/pkg/dispatcher/message"
	"go.uber.org/zap"
)

var logger *zap.Logger

type Dispatcher struct {
	config                 *configs.Config
	connector              *connector.Connector
	dataProductConfigStore *config_store.ConfigStore
	dataProductManager     *DataProductManager
	watcher                *EventWatcher
	processor              *Processor
}

func New(config *configs.Config, l *zap.Logger, c *connector.Connector) *Dispatcher {

	logger = l

	d := &Dispatcher{
		config:    config,
		connector: c,
	}

	d.dataProductManager = NewDataProductManager(d)
	d.processor = NewProcessor(
		WithOutputHandler(func(msg *message.Message) {
			d.dispatch(msg)
		}),
	)
	d.dataProductConfigStore = config_store.NewConfigStore(c,
		config_store.WithDomain(c.GetDomain()),
		config_store.WithCatalog("COLLECTION"),
		config_store.WithEventHandler(d.dataProductSettingsUpdated),
	)

	err := d.initialize()
	if err != nil {
		logger.Error(err.Error())
		return nil
	}

	return d
}

func (d *Dispatcher) dataProductSettingsUpdated(op config_store.ConfigOp, dataProductName string, data []byte) {

	logger.Info("Syncing data product settings",
		zap.String("dataProduct", dataProductName),
	)

	var setting DataProductSetting
	err := json.Unmarshal(data, &setting)
	if err != nil {
		logger.Error(err.Error())
		return
	}

	// Delete dataProduct
	if op == config_store.ConfigDelete {
		d.dataProductManager.DeleteDataProduct(dataProductName)
		return
	}

	// Create or update dataProduct
	err = d.dataProductManager.ApplySettings(dataProductName, &setting)
	if err != nil {
		logger.Error("Failed to load data product settings",
			zap.String("data product", dataProductName),
		)
		logger.Error(err.Error())
		return
	}
}

func (d *Dispatcher) initialize() error {

	err := d.dataProductConfigStore.Init()
	if err != nil {
		return err
	}

	return nil
}

/*
func (d *Dispatcher) registerEvents() {

	// Default events
	for _, e := range d.config.Events {
		logger.Info(fmt.Sprintf("Regiserted event: %s", e))
		d.watcher.RegisterEvent(e)
	}
}
*/
func (d *Dispatcher) dispatch(msg *message.Message) {

	subject := fmt.Sprintf("GRAVITY.%s.DP.%s.%d.EVENT.%s",
		d.connector.GetDomain(),
		msg.Record.Table,
		msg.Partition,
		msg.Record.EventName,
	)

	// Preparing JetStream
	nc := d.connector.GetClient().GetConnection()
	js, err := nc.JetStream()
	if err != nil {
		logger.Error(err.Error())
		return
	}

	go func() {

		// Publish to dataProduct stream
		future, err := js.PublishAsync(subject, msg.RawRecord)
		if err != nil {
			logger.Error(err.Error())
			return
		}

		<-future.Ok()

		// Acknowledge
		msg.Msg.Ack()

		msg.Release()
	}()
}
