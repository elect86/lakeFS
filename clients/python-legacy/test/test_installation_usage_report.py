"""
    lakeFS API

    lakeFS HTTP API  # noqa: E501

    The version of the OpenAPI document: 1.0.0
    Contact: services@treeverse.io
    Generated by: https://openapi-generator.tech
"""


import sys
import unittest

import lakefs_client
from lakefs_client.model.usage_report import UsageReport
globals()['UsageReport'] = UsageReport
from lakefs_client.model.installation_usage_report import InstallationUsageReport


class TestInstallationUsageReport(unittest.TestCase):
    """InstallationUsageReport unit test stubs"""

    def setUp(self):
        pass

    def tearDown(self):
        pass

    def testInstallationUsageReport(self):
        """Test InstallationUsageReport"""
        # FIXME: construct object with mandatory attributes with example values
        # model = InstallationUsageReport()  # noqa: E501
        pass


if __name__ == '__main__':
    unittest.main()