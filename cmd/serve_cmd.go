package cmd

import (
	"context"
	"fmt"

	"github.com/netlify/gocommerce/api"
	"github.com/netlify/gocommerce/conf"
	"github.com/netlify/gocommerce/models"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var serveCmd = cobra.Command{
	Use:  "serve",
	Long: "Start API server",
	Run: func(cmd *cobra.Command, args []string) {
		execWithConfig(cmd, serve)
	},
}

func serve(globalConfig *conf.GlobalConfiguration, log logrus.FieldLogger, config *conf.Configuration) {
	db, err := models.Connect(globalConfig, log.WithField("component", "db"))
	if err != nil {
		log.Fatalf("Error opening database: %+v", err)
	}
	defer db.Close()

	bgDB, err := models.Connect(globalConfig, log.WithField("component", "db").WithField("bgdb", true))
	if err != nil {
		log.Fatalf("Error opening database: %+v", err)
	}
	defer bgDB.Close()

	ctx, err := api.WithInstanceConfig(context.Background(), globalConfig.SMTP, config, "", log)
	if err != nil {
		log.Fatalf("Error loading instance config: %+v", err)
	}
	api := api.NewAPIWithVersion(ctx, globalConfig, log, db, Version)

	l := fmt.Sprintf("%v:%v", globalConfig.API.Host, globalConfig.API.Port)
	log.Infof("GoCommerce API started on: %s", l)

	models.RunHooks(bgDB, log.WithField("component", "hooks"))

	api.ListenAndServe(l)
}
