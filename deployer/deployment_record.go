package deployer

import (
	bosherr "github.com/cloudfoundry/bosh-agent/errors"

	bmconfig "github.com/cloudfoundry/bosh-micro-cli/config"
	bmcrypto "github.com/cloudfoundry/bosh-micro-cli/crypto"
	bmstemcell "github.com/cloudfoundry/bosh-micro-cli/deployer/stemcell"
	bmrel "github.com/cloudfoundry/bosh-micro-cli/release"
)

type DeploymentRecord interface {
	IsDeployed(manifestPath string, release bmrel.Release, stemcell bmstemcell.ExtractedStemcell) (bool, error)
	Update(manifestPath string, release bmrel.Release, stemcell bmstemcell.ExtractedStemcell) error
}

type deploymentRecord struct {
	deploymentRepo bmconfig.DeploymentRepo
	releaseRepo    bmconfig.ReleaseRepo
	stemcellRepo   bmconfig.StemcellRepo
	sha1Calculator bmcrypto.SHA1Calculator
}

func NewDeploymentRecord(
	deploymentRepo bmconfig.DeploymentRepo,
	releaseRepo bmconfig.ReleaseRepo,
	stemcellRepo bmconfig.StemcellRepo,
	sha1Calculator bmcrypto.SHA1Calculator,
) DeploymentRecord {
	return &deploymentRecord{
		deploymentRepo: deploymentRepo,
		releaseRepo:    releaseRepo,
		stemcellRepo:   stemcellRepo,
		sha1Calculator: sha1Calculator,
	}
}

func (v *deploymentRecord) IsDeployed(manifestPath string, release bmrel.Release, stemcell bmstemcell.ExtractedStemcell) (bool, error) {
	manifestSHA1, found, err := v.deploymentRepo.FindCurrent()
	if err != nil {
		return false, bosherr.WrapError(err, "Finding sha1 of currently deployed manifest")
	}

	if !found {
		return false, nil
	}

	newSHA1, err := v.sha1Calculator.Calculate(manifestPath)
	if err != nil {
		return false, bosherr.WrapError(err, "Calculating sha1 of current deployment manifest")
	}

	if manifestSHA1 != newSHA1 {
		return false, nil
	}

	currentStemcell, found, err := v.stemcellRepo.FindCurrent()
	if err != nil {
		return false, bosherr.WrapError(err, "Finding currently deployed stemcell")
	}

	if !found {
		return false, nil
	}

	if currentStemcell.Name != stemcell.Manifest().Name || currentStemcell.Version != stemcell.Manifest().Version {
		return false, nil
	}

	currentRelease, found, err := v.releaseRepo.FindCurrent()
	if err != nil {
		return false, bosherr.WrapError(err, "Finding currently deployed release")
	}

	if !found {
		return false, nil
	}

	if currentRelease.Name != release.Name() || currentRelease.Version != release.Version() {
		return false, nil
	}

	return true, nil
}

func (v *deploymentRecord) Update(manifestPath string, release bmrel.Release, stemcell bmstemcell.ExtractedStemcell) error {
	manifestSHA1, err := v.sha1Calculator.Calculate(manifestPath)
	if err != nil {
		return bosherr.WrapError(err, "Calculating sha1 of current deployment manifest")
	}

	err = v.deploymentRepo.UpdateCurrent(manifestSHA1)
	if err != nil {
		return bosherr.WrapError(err, "Saving sha1 of deployed manifest")
	}

	releaseRecord, found, err := v.releaseRepo.Find(release.Name(), release.Version())
	if !found {
		releaseRecord, err = v.releaseRepo.Save(release.Name(), release.Version())
		if err != nil {
			return bosherr.WrapError(err, "Saving release record with name: '%s', version: '%s'", release.Name(), release.Version())
		}
	}

	err = v.releaseRepo.UpdateCurrent(releaseRecord.ID)
	if err != nil {
		return bosherr.WrapError(err, "Updating current release record")
	}

	stemcellManifest := stemcell.Manifest()
	stemcellRecord, found, err := v.stemcellRepo.Find(stemcellManifest.Name, stemcellManifest.Version)
	if err != nil {
		return bosherr.WrapError(err, "Finding stemcell record with name: '%s', version: '%s'", stemcellManifest.Name, stemcellManifest.Version)
	}

	if !found {
		return bosherr.New("Stemcell record not found with name '%s', version: '%s'", stemcellManifest.Name, stemcellManifest.Version)
	}

	err = v.stemcellRepo.UpdateCurrent(stemcellRecord.ID)
	if err != nil {
		return bosherr.WrapError(err, "Updating current stemcell record")
	}

	return nil
}
