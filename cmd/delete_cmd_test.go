package cmd_test

import (
	. "github.com/cloudfoundry/bosh-micro-cli/cmd"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"errors"

	"code.google.com/p/gomock/gomock"
	mock_cloud "github.com/cloudfoundry/bosh-micro-cli/cloud/mocks"
	mock_agentclient "github.com/cloudfoundry/bosh-micro-cli/deployer/agentclient/mocks"

	boshlog "github.com/cloudfoundry/bosh-agent/logger"
	boshsys "github.com/cloudfoundry/bosh-agent/system"
	fakesys "github.com/cloudfoundry/bosh-agent/system/fakes"
	fakeuuid "github.com/cloudfoundry/bosh-agent/uuid/fakes"

	bmconfig "github.com/cloudfoundry/bosh-micro-cli/config"
	bmdisk "github.com/cloudfoundry/bosh-micro-cli/deployer/disk"
	bminstance "github.com/cloudfoundry/bosh-micro-cli/deployer/instance"
	bmregistry "github.com/cloudfoundry/bosh-micro-cli/deployer/registry"
	bmsshtunnel "github.com/cloudfoundry/bosh-micro-cli/deployer/sshtunnel"
	bmvm "github.com/cloudfoundry/bosh-micro-cli/deployer/vm"
	bmdepl "github.com/cloudfoundry/bosh-micro-cli/deployment"
	bmeventlog "github.com/cloudfoundry/bosh-micro-cli/eventlogger"
	bmrel "github.com/cloudfoundry/bosh-micro-cli/release"

	fakebmcpi "github.com/cloudfoundry/bosh-micro-cli/cpi/fakes"
	fakebmas "github.com/cloudfoundry/bosh-micro-cli/deployer/applyspec/fakes"
	fakeui "github.com/cloudfoundry/bosh-micro-cli/ui/fakes"
)

var _ = Describe("Cmd/DeleteCmd", func() {
	var mockCtrl *gomock.Controller

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	Describe("Run", func() {
		var (
			fs                      boshsys.FileSystem
			logger                  boshlog.Logger
			fakeCPIInstaller        *fakebmcpi.FakeInstaller
			fakeUUIDGenerator       *fakeuuid.FakeGenerator
			deploymentConfigService bmconfig.DeploymentConfigService
			vmRepo                  bmconfig.VMRepo
			diskRepo                bmconfig.DiskRepo
			stemcellRepo            bmconfig.StemcellRepo
			userConfig              bmconfig.UserConfig

			ui *fakeui.FakeUI

			fakeApplySpecFactory       *fakebmas.FakeApplySpecFactory
			fakeTemplatesSpecGenerator *fakebmas.FakeTemplatesSpecGenerator

			mockAgentClient        *mock_agentclient.MockAgentClient
			mockAgentClientFactory *mock_agentclient.MockFactory
			mockCloud              *mock_cloud.MockCloud
			deploymentManifestPath = "/deployment-dir/fake-deployment-manifest.yml"
			deploymentConfigPath   = "/fake-bosh-deployments.json"
		)

		var writeDeploymentManifest = func() {
			fs.WriteFileString(deploymentManifestPath, `---
name: test-release

cloud_provider:
  mbus: http://fake-mbus-url
`)
		}

		var writeCPIReleaseTarball = func() {
			fs.WriteFileString("/fake-cpi-release.tgz", "fake-tgz-content")
		}

		var allowCPIToBeExtracted = func() {
			cpiRelease := bmrel.NewRelease(
				"fake-cpi-release-name",
				"fake-cpi-release-version",
				[]bmrel.Job{},
				[]*bmrel.Package{},
				"fake-extracted-dir",
				fs,
			)
			fakeCPIInstaller.SetExtractBehavior("/fake-cpi-release.tgz", cpiRelease, nil)
		}

		var allowCPIToBeInstalled = func() {
			cpiRelease := bmrel.NewRelease(
				"fake-cpi-release-name",
				"fake-cpi-release-version",
				[]bmrel.Job{},
				[]*bmrel.Package{},
				"fake-extracted-dir",
				fs,
			)
			cpiDeployment := bmdepl.CPIDeployment{
				Name: "test-release",
				Mbus: "http://fake-mbus-url",
			}
			fakeCPIInstaller.SetInstallBehavior(cpiDeployment, cpiRelease, mockCloud, nil)
		}

		var newDeleteCmd = func() Cmd {
			deploymentParser := bmdepl.NewParser(fs, logger)
			vmManagerFactory := bmvm.NewManagerFactory(
				vmRepo,
				stemcellRepo,
				mockAgentClientFactory,
				fakeApplySpecFactory,
				fakeTemplatesSpecGenerator,
				fs,
				logger,
			)
			registryServer := bmregistry.NewServer(logger)
			sshTunnelFactory := bmsshtunnel.NewFactory(logger)
			diskManagerFactory := bmdisk.NewManagerFactory(diskRepo, logger)
			diskDeployer := bminstance.NewDiskDeployer(diskManagerFactory, diskRepo, logger)
			instanceManagerFactory := bminstance.NewManagerFactory(
				registryServer,
				sshTunnelFactory,
				diskDeployer,
				logger,
			)
			eventLogger := bmeventlog.NewEventLogger(ui)
			return NewDeleteCmd(
				ui, userConfig, fs, deploymentParser, fakeCPIInstaller,
				vmManagerFactory, instanceManagerFactory,
				vmRepo, diskRepo, stemcellRepo, mockAgentClientFactory,
				eventLogger, logger,
			)
		}

		var expectNormalFlow = func() {
			gomock.InOrder(
				mockAgentClientFactory.EXPECT().Create("http://fake-mbus-url").Return(mockAgentClient),
				mockAgentClient.EXPECT().Ping().Return("any-state", nil),                   // ping to make sure agent is responsive
				mockAgentClient.EXPECT().Stop(),                                            // stop all jobs
				mockAgentClient.EXPECT().ListDisk().Return([]string{"fake-disk-cid"}, nil), // get mounted disks to be unmounted
				mockAgentClient.EXPECT().UnmountDisk("fake-disk-cid"),
				mockCloud.EXPECT().DeleteVM("fake-vm-cid"),
				mockCloud.EXPECT().DeleteDisk("fake-disk-cid"),
				mockCloud.EXPECT().DeleteStemcell("fake-stemcell-cid"),
			)
		}

		BeforeEach(func() {
			fs = fakesys.NewFakeFileSystem()
			logger = boshlog.NewLogger(boshlog.LevelNone)
			deploymentConfigService = bmconfig.NewFileSystemDeploymentConfigService(deploymentConfigPath, fs, logger)
			fakeUUIDGenerator = fakeuuid.NewFakeGenerator()

			vmRepo = bmconfig.NewVMRepo(deploymentConfigService)
			diskRepo = bmconfig.NewDiskRepo(deploymentConfigService, fakeUUIDGenerator)
			stemcellRepo = bmconfig.NewStemcellRepo(deploymentConfigService, fakeUUIDGenerator)

			mockCloud = mock_cloud.NewMockCloud(mockCtrl)

			fakeCPIInstaller = fakebmcpi.NewFakeInstaller()

			ui = &fakeui.FakeUI{}

			mockAgentClientFactory = mock_agentclient.NewMockFactory(mockCtrl)
			mockAgentClient = mock_agentclient.NewMockAgentClient(mockCtrl)

			userConfig = bmconfig.UserConfig{DeploymentFile: deploymentManifestPath}

			writeDeploymentManifest()
			writeCPIReleaseTarball()
			allowCPIToBeExtracted()
			allowCPIToBeInstalled()
		})

		Context("when the deployment has not been set", func() {
			BeforeEach(func() {
				userConfig.DeploymentFile = ""
			})

			It("returns an error", func() {
				err := newDeleteCmd().Run([]string{"/fake-cpi-release.tgz"})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("No deployment set"))
			})
		})

		Context("when the deployment config file does not exist", func() {
			BeforeEach(func() {
				err := fs.RemoveAll(deploymentConfigPath)
				Expect(err).ToNot(HaveOccurred())
			})

			It("does not delete anything", func() {
				err := newDeleteCmd().Run([]string{"/fake-cpi-release.tgz"})
				Expect(err).ToNot(HaveOccurred())

				Expect(ui.Said).To(Equal([]string{
					"Started validating",
					"Started validating > Validating deployment manifest...", " done. (00:00:00)",
					"Started validating > Validating cpi release...", " done. (00:00:00)",
					"Done validating",
					"",
					// if cpiInstaller were not mocked, it would print the "installing CPI jobs" stage here.
					"Started deleting deployment",
					"Done deleting deployment",
					"",
				}))
			})
		})

		Context("when the deployment has been deployed", func() {
			BeforeEach(func() {
				// create deployment manifest yaml file
				deploymentConfigService.Save(bmconfig.DeploymentFile{
					UUID:              "",
					CurrentVMCID:      "fake-vm-cid",
					CurrentStemcellID: "fake-stemcell-guid",
					CurrentDiskID:     "fake-disk-guid",
					Disks: []bmconfig.DiskRecord{
						{
							ID:   "fake-disk-guid",
							CID:  "fake-disk-cid",
							Size: 100,
						},
					},
					Stemcells: []bmconfig.StemcellRecord{
						{
							ID:  "fake-stemcell-guid",
							CID: "fake-stemcell-cid",
						},
					},
				})
			})

			It("stops agent, unmounts disk, deletes vm, deletes disk, deletes stemcell", func() {
				expectNormalFlow()

				err := newDeleteCmd().Run([]string{"/fake-cpi-release.tgz"})
				Expect(err).ToNot(HaveOccurred())
			})

			It("logs validation stages", func() {
				expectNormalFlow()

				err := newDeleteCmd().Run([]string{"/fake-cpi-release.tgz"})
				Expect(err).ToNot(HaveOccurred())

				Expect(ui.Said).To(Equal([]string{
					"Started validating",
					"Started validating > Validating deployment manifest...", " done. (00:00:00)",
					"Started validating > Validating cpi release...", " done. (00:00:00)",
					"Done validating",
					"",
					// if cpiInstaller were not mocked, it would print the "installing CPI jobs" stage here.
					"Started deleting deployment",
					"Started deleting deployment > Waiting for the agent on VM 'fake-vm-cid'...", " done. (00:00:00)",
					"Started deleting deployment > Stopping jobs on instance 'unknown/0'...", " done. (00:00:00)",
					"Started deleting deployment > Unmounting disk 'fake-disk-cid'...", " done. (00:00:00)",
					"Started deleting deployment > Deleting VM 'fake-vm-cid'...", " done. (00:00:00)",
					"Started deleting deployment > Deleting disk 'fake-disk-cid'...", " done. (00:00:00)",
					"Started deleting deployment > Deleting stemcell 'fake-stemcell-cid'...", " done. (00:00:00)",
					"Done deleting deployment",
					"",
				}))
			})

			It("clears current vm, disk and stemcell", func() {
				expectNormalFlow()

				err := newDeleteCmd().Run([]string{"/fake-cpi-release.tgz"})
				Expect(err).ToNot(HaveOccurred())

				_, found, err := vmRepo.FindCurrent()
				Expect(found).To(BeFalse(), "should be no current VM")

				_, found, err = diskRepo.FindCurrent()
				Expect(found).To(BeFalse(), "should be no current disk")

				diskRecords, err := diskRepo.All()
				Expect(err).ToNot(HaveOccurred())
				Expect(diskRecords).To(BeEmpty(), "expected no disk records")

				_, found, err = stemcellRepo.FindCurrent()
				Expect(found).To(BeFalse(), "should be no current stemcell")

				stemcellRecords, err := stemcellRepo.All()
				Expect(err).ToNot(HaveOccurred())
				Expect(stemcellRecords).To(BeEmpty(), "expected no stemcell records")
			})

			Context("when agent is unresponsive", func() {
				It("times out pinging agent, deletes vm, deletes disk, deletes stemcell", func() {
					gomock.InOrder(
						mockAgentClientFactory.EXPECT().Create("http://fake-mbus-url").Return(mockAgentClient),
						mockAgentClient.EXPECT().Ping().Return("", errors.New("unresponsive agent")).AnyTimes(), // ping to make sure agent is responsive
						mockCloud.EXPECT().DeleteVM("fake-vm-cid"),
						mockCloud.EXPECT().DeleteDisk("fake-disk-cid"),
						mockCloud.EXPECT().DeleteStemcell("fake-stemcell-cid"),
					)

					err := newDeleteCmd().Run([]string{"/fake-cpi-release.tgz"})
					Expect(err).ToNot(HaveOccurred())
				})
			})

			Context("and delete previously suceeded", func() {
				BeforeEach(func() {
					expectNormalFlow()

					err := newDeleteCmd().Run([]string{"/fake-cpi-release.tgz"})
					Expect(err).ToNot(HaveOccurred())

					// reset ui output
					ui.Said = []string{}
				})

				It("does not delete anything", func() {
					err := newDeleteCmd().Run([]string{"/fake-cpi-release.tgz"})
					Expect(err).ToNot(HaveOccurred())

					Expect(ui.Said).To(Equal([]string{
						"Started validating",
						"Started validating > Validating deployment manifest...", " done. (00:00:00)",
						"Started validating > Validating cpi release...", " done. (00:00:00)",
						"Done validating",
						"",
						// if cpiInstaller were not mocked, it would print the "installing CPI jobs" stage here.
						"Started deleting deployment",
						"Done deleting deployment",
						"",
					}))
				})
			})

			Context("and orphan disks exist", func() {
				BeforeEach(func() {
					_, err := diskRepo.Save("orphan-disk-cid-2", 100, nil)
					Expect(err).ToNot(HaveOccurred())
				})

				It("deletes the orphaned disks", func() {
					expectNormalFlow()

					mockCloud.EXPECT().DeleteDisk("orphan-disk-cid-2")

					err := newDeleteCmd().Run([]string{"/fake-cpi-release.tgz"})
					Expect(err).ToNot(HaveOccurred())

					diskRecords, err := diskRepo.All()
					Expect(err).ToNot(HaveOccurred())
					Expect(diskRecords).To(BeEmpty(), "expected no disk records")
				})

				It("logs validation stages", func() {
					expectNormalFlow()

					mockCloud.EXPECT().DeleteDisk("orphan-disk-cid-2")

					err := newDeleteCmd().Run([]string{"/fake-cpi-release.tgz"})
					Expect(err).ToNot(HaveOccurred())

					Expect(ui.Said).To(Equal([]string{
						"Started validating",
						"Started validating > Validating deployment manifest...", " done. (00:00:00)",
						"Started validating > Validating cpi release...", " done. (00:00:00)",
						"Done validating",
						"",
						// if cpiInstaller were not mocked, it would print the "installing CPI jobs" stage here.
						"Started deleting deployment",
						"Started deleting deployment > Waiting for the agent on VM 'fake-vm-cid'...", " done. (00:00:00)",
						"Started deleting deployment > Stopping jobs on instance 'unknown/0'...", " done. (00:00:00)",
						"Started deleting deployment > Unmounting disk 'fake-disk-cid'...", " done. (00:00:00)",
						"Started deleting deployment > Deleting VM 'fake-vm-cid'...", " done. (00:00:00)",
						"Started deleting deployment > Deleting disk 'fake-disk-cid'...", " done. (00:00:00)",
						"Started deleting deployment > Deleting stemcell 'fake-stemcell-cid'...", " done. (00:00:00)",
						"Started deleting deployment > Deleting orphaned disk 'orphan-disk-cid-2'...", " done. (00:00:00)",
						"Done deleting deployment",
						"",
					}))
				})
			})

			Context("and orphan stemcells exist", func() {
				BeforeEach(func() {
					_, err := stemcellRepo.Save("orphan-stemcell-name-2", "orphan-stemcell-version-2", "orphan-stemcell-cid-2")
					Expect(err).ToNot(HaveOccurred())
				})

				It("deletes the orphaned stemcells", func() {
					expectNormalFlow()

					mockCloud.EXPECT().DeleteStemcell("orphan-stemcell-cid-2")

					err := newDeleteCmd().Run([]string{"/fake-cpi-release.tgz"})
					Expect(err).ToNot(HaveOccurred())

					stemcellRecords, err := stemcellRepo.All()
					Expect(err).ToNot(HaveOccurred())
					Expect(stemcellRecords).To(BeEmpty(), "expected no stemcell records")
				})

				It("logs validation stages", func() {
					expectNormalFlow()

					mockCloud.EXPECT().DeleteStemcell("orphan-stemcell-cid-2")

					err := newDeleteCmd().Run([]string{"/fake-cpi-release.tgz"})
					Expect(err).ToNot(HaveOccurred())

					Expect(ui.Said).To(Equal([]string{
						"Started validating",
						"Started validating > Validating deployment manifest...", " done. (00:00:00)",
						"Started validating > Validating cpi release...", " done. (00:00:00)",
						"Done validating",
						"",
						// if cpiInstaller were not mocked, it would print the compilation and installation stages here.
						"Started deleting deployment",
						"Started deleting deployment > Waiting for the agent on VM 'fake-vm-cid'...", " done. (00:00:00)",
						"Started deleting deployment > Stopping jobs on instance 'unknown/0'...", " done. (00:00:00)",
						"Started deleting deployment > Unmounting disk 'fake-disk-cid'...", " done. (00:00:00)",
						"Started deleting deployment > Deleting VM 'fake-vm-cid'...", " done. (00:00:00)",
						"Started deleting deployment > Deleting disk 'fake-disk-cid'...", " done. (00:00:00)",
						"Started deleting deployment > Deleting stemcell 'fake-stemcell-cid'...", " done. (00:00:00)",
						"Started deleting deployment > Deleting orphaned stemcell 'orphan-stemcell-cid-2'...", " done. (00:00:00)",
						"Done deleting deployment",
						"",
					}))
				})
			})
		})

		Context("when nothing has been deployed", func() {
			BeforeEach(func() {
				deploymentConfigService.Save(bmconfig.DeploymentFile{})
			})

			It("returns an error", func() {
				err := newDeleteCmd().Run([]string{"/fake-cpi-release.tgz"})
				Expect(err).NotTo(HaveOccurred())
				Expect(ui.Errors).To(BeEmpty())
			})

			Context("when there are orphans", func() {
				BeforeEach(func() {
					diskRepo.Save("orphan-disk-cid", 1, nil)
					stemcellRepo.Save("orphan-stemcell-name", "orphan-stemcell-version", "orphan-stemcell-cid")
					stemcellRepo.Save("orphan-stemcell-name", "orphan-stemcell-version-2", "orphan-stemcell-cid-2")
				})

				It("deletes the orphans", func() {
					mockCloud.EXPECT().DeleteDisk("orphan-disk-cid")
					mockCloud.EXPECT().DeleteStemcell("orphan-stemcell-cid")
					mockCloud.EXPECT().DeleteStemcell("orphan-stemcell-cid-2")

					err := newDeleteCmd().Run([]string{"/fake-cpi-release.tgz"})
					Expect(err).NotTo(HaveOccurred())

					diskRecords, err := diskRepo.All()
					Expect(err).ToNot(HaveOccurred())
					Expect(diskRecords).To(BeEmpty(), "expected no disk records")

					stemcellRecords, err := stemcellRepo.All()
					Expect(err).ToNot(HaveOccurred())
					Expect(stemcellRecords).To(BeEmpty(), "expected no stemcell records")

					Expect(ui.Errors).To(BeEmpty())
					Expect(ui.Said).To(Equal([]string{
						"Started validating",
						"Started validating > Validating deployment manifest...", " done. (00:00:00)",
						"Started validating > Validating cpi release...", " done. (00:00:00)",
						"Done validating",
						"",
						// if cpiInstaller were not mocked, it would print the compilation and installation stages here.
						"Started deleting deployment",
						"Started deleting deployment > Deleting orphaned disk 'orphan-disk-cid'...", " done. (00:00:00)",
						"Started deleting deployment > Deleting orphaned stemcell 'orphan-stemcell-cid'...", " done. (00:00:00)",
						"Started deleting deployment > Deleting orphaned stemcell 'orphan-stemcell-cid-2'...", " done. (00:00:00)",
						"Done deleting deployment",
						"",
					}))
				})
			})
		})

		Context("when VM has been deployed", func() {
			BeforeEach(func() {
				deploymentConfigService.Save(bmconfig.DeploymentFile{})
				vmRepo.UpdateCurrent("fake-vm-cid")
			})

			It("stops the agent and deletes the VM", func() {
				gomock.InOrder(
					mockAgentClientFactory.EXPECT().Create("http://fake-mbus-url").Return(mockAgentClient),
					mockAgentClient.EXPECT().Ping().Return("any-state", nil),                   // ping to make sure agent is responsive
					mockAgentClient.EXPECT().Stop(),                                            // stop all jobs
					mockAgentClient.EXPECT().ListDisk().Return([]string{"fake-disk-cid"}, nil), // get mounted disks to be unmounted
					mockAgentClient.EXPECT().UnmountDisk("fake-disk-cid"),
					mockCloud.EXPECT().DeleteVM("fake-vm-cid"),
				)
				//TODO: expectNormalFlow()

				err := newDeleteCmd().Run([]string{"/fake-cpi-release.tgz"})
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("when a current disk exists", func() {
			BeforeEach(func() {
				deploymentConfigService.Save(bmconfig.DeploymentFile{})
				diskRecord, err := diskRepo.Save("fake-disk-cid", 100, nil)
				Expect(err).ToNot(HaveOccurred())
				diskRepo.UpdateCurrent(diskRecord.ID)
			})

			It("deletes the disk", func() {
				mockCloud.EXPECT().DeleteDisk("fake-disk-cid")

				err := newDeleteCmd().Run([]string{"/fake-cpi-release.tgz"})
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("when a current stemcell exists", func() {
			BeforeEach(func() {
				deploymentConfigService.Save(bmconfig.DeploymentFile{})
				stemcellRecord, err := stemcellRepo.Save("fake-stemcell-name", "fake-stemcell-version", "fake-stemcell-cid")
				Expect(err).ToNot(HaveOccurred())
				stemcellRepo.UpdateCurrent(stemcellRecord.ID)
			})

			It("deletes the stemcell", func() {
				mockCloud.EXPECT().DeleteStemcell("fake-stemcell-cid")

				err := newDeleteCmd().Run([]string{"/fake-cpi-release.tgz"})
				Expect(err).ToNot(HaveOccurred())
			})
		})
	})
})
