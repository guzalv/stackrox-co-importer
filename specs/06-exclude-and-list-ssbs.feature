Feature: SSB exclusion filtering and discovery listing
  As an operator
  I want to exclude specific SSBs and list what would be imported
  So that I can selectively import and preview before committing

  # ─── IMP-CLI-028: --exclude filtering ───────────────────────────────────────

  # IMP-CLI-028
  Scenario: --exclude exact name removes matching SSB
    Given SSBs "ocp4-cis", "ocp4-pci-dss", "rhcos4-e8" exist in namespace "openshift-compliance"
    When exclude pattern "ocp4-cis" is applied
    Then the remaining SSBs are "ocp4-pci-dss,rhcos4-e8"

  # IMP-CLI-028
  Scenario: --exclude regex matches by prefix
    Given SSBs "ocp4-cis", "ocp4-pci-dss", "rhcos4-e8" exist in namespace "openshift-compliance"
    When exclude pattern "ocp4-.*" is applied
    Then the remaining SSBs are "rhcos4-e8"

  # IMP-CLI-028
  Scenario: --exclude with no matches leaves all SSBs
    Given SSBs "ocp4-cis", "ocp4-pci-dss" exist in namespace "openshift-compliance"
    When exclude pattern "rhcos4-.*" is applied
    Then the remaining SSBs are "ocp4-cis,ocp4-pci-dss"

  # IMP-CLI-028
  Scenario: Multiple --exclude patterns are OR-ed
    Given SSBs "ocp4-cis", "ocp4-pci-dss", "rhcos4-e8" exist in namespace "openshift-compliance"
    When exclude patterns "ocp4-cis,rhcos4-e8" are applied
    Then the remaining SSBs are "ocp4-pci-dss"

  # IMP-CLI-028
  Scenario: --exclude with empty pattern list keeps all SSBs
    Given SSBs "ocp4-cis", "ocp4-pci-dss" exist in namespace "openshift-compliance"
    When no exclude patterns are applied
    Then the remaining SSBs are "ocp4-cis,ocp4-pci-dss"

  # ─── IMP-CLI-029: --list-ssbs output ─────────────────────────────────────────

  # IMP-CLI-029
  Scenario: --list-ssbs prints namespace/name one per line sorted
    Given SSBs "rhcos4-e8", "ocp4-cis" exist in namespace "openshift-compliance"
    When SSB list output is requested with no exclude patterns
    Then the output lines are "openshift-compliance/ocp4-cis,openshift-compliance/rhcos4-e8"

  # IMP-CLI-029
  Scenario: --list-ssbs with --exclude filters the output
    Given SSBs "ocp4-cis", "ocp4-pci-dss", "rhcos4-e8" exist in namespace "openshift-compliance"
    When SSB list output is requested with exclude pattern "ocp4-.*"
    Then the output lines are "openshift-compliance/rhcos4-e8"

  # IMP-CLI-029
  Scenario: --list-ssbs with no SSBs produces empty output
    Given no SSBs exist
    When SSB list output is requested with no exclude patterns
    Then the output is empty
