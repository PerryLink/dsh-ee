import os

from conan import ConanFile
from conan.tools.files import copy


class RegistryDemo(ConanFile):
    name = "dsh-ee-demo"
    version = "1.0"
    package_type = "header-library"
    exports = "LICENSE"
    exports_sources = "include/*"

    def package(self):
        copy(self, "*.h", src=os.path.join(self.source_folder, "include"),
             dst=os.path.join(self.package_folder, "include"))

    def package_id(self):
        self.info.clear()
