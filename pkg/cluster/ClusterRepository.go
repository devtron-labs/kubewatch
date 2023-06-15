package repository

import (
	"github.com/devtron-labs/kubewatch/pkg/sql"
	"github.com/go-pg/pg"
	"go.uber.org/zap"
)

type Cluster struct {
	tableName              struct{}          `sql:"cluster" pg:",discard_unknown_columns"`
	Id                     int               `sql:"id,pk"`
	ClusterName            string            `sql:"cluster_name"`
	ServerUrl              string            `sql:"server_url"`
	PrometheusEndpoint     string            `sql:"prometheus_endpoint"`
	Active                 bool              `sql:"active,notnull"`
	CdArgoSetup            bool              `sql:"cd_argo_setup,notnull"`
	Config                 map[string]string `sql:"config"`
	PUserName              string            `sql:"p_username"`
	PPassword              string            `sql:"p_password"`
	PTlsClientCert         string            `sql:"p_tls_client_cert"`
	PTlsClientKey          string            `sql:"p_tls_client_key"`
	AgentInstallationStage int               `sql:"agent_installation_stage"`
	K8sVersion             string            `sql:"k8s_version"`
	ErrorInConnecting      string            `sql:"error_in_connecting"`
	sql.AuditLog
}

func NewClusterRepositoryImpl(dbConnection *pg.DB, logger *zap.SugaredLogger) *ClusterRepositoryImpl {
	return &ClusterRepositoryImpl{
		dbConnection: dbConnection,
		logger:       logger,
	}
}

type ClusterRepositoryImpl struct {
	dbConnection *pg.DB
	logger       *zap.SugaredLogger
}

type ClusterRepository interface {
	FindAllActive() ([]*Cluster, error)
	FindById(id int) (*Cluster, error)
	FindByIdWithActiveFalse(id int) (*Cluster, error)
}

func (impl ClusterRepositoryImpl) FindAllActive() ([]*Cluster, error) {
	var clusters []*Cluster
	err := impl.dbConnection.
		Model(&clusters).
		Where("active=?", true).
		Select()
	return clusters, err
}

func (impl ClusterRepositoryImpl) FindById(id int) (*Cluster, error) {
	var cluster Cluster
	err := impl.dbConnection.
		Model(&cluster).
		Where("id= ? ", id).
		Where("active =?", true).
		Select()
	return &cluster, err
}

func (impl ClusterRepositoryImpl) FindByIdWithActiveFalse(id int) (*Cluster, error) {
	var cluster Cluster
	err := impl.dbConnection.
		Model(&cluster).
		Where("id= ? ", id).
		Where("active =?", false).
		Select()
	return &cluster, err
}
