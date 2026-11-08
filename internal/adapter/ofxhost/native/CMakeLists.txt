# Builds the OpenFX host as one static library for the Go side to link.
#
# cgo compiles the Go package's own C++ files, but HostSupport is a separate C++
# library with its own translation units, and cgo cannot compile files that live
# outside the package directory. This project produces libofxhost.a from host.cpp
# plus HostSupport, and the Go build links it.
#
# Usage:
#   cmake -S internal/adapter/ofxhost/native -B build/ofxhost-native -DCMAKE_BUILD_TYPE=Release
#   cmake --build build/ofxhost-native -j
#
# The output is picked up by cgo through the LDFLAGS in the package's darwin and
# linux files.
cmake_minimum_required(VERSION 3.20)

# The same floor the Go build declares through -mmacosx-version-min, set BEFORE project() because
# CMake reads it when it initialises the toolchain. Without it the library inherits the SDK's
# default, which on a current machine is several major versions above the floor the app claims:
# the objects came out built for macOS 27 and the linker warned on every one of them. A library
# that says 27 cannot be linked into an app that says 12 and mean it.
if(APPLE AND NOT DEFINED CMAKE_OSX_DEPLOYMENT_TARGET)
  set(CMAKE_OSX_DEPLOYMENT_TARGET "12.0" CACHE STRING "Minimum macOS version")
endif()

project(ofxhost_native LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)

if(NOT CMAKE_BUILD_TYPE)
  set(CMAKE_BUILD_TYPE Release)
endif()

set(OFX_ROOT "${CMAKE_CURRENT_SOURCE_DIR}/../../../../third_party/openfx")
set(OFX_INCLUDE "${OFX_ROOT}/include")
set(OFX_HOST_INCLUDE "${OFX_ROOT}/HostSupport/include")
set(OFX_HOST_SRC "${OFX_ROOT}/HostSupport/src")

if(NOT EXISTS "${OFX_INCLUDE}/ofxCore.h")
  message(FATAL_ERROR
    "The OpenFX submodule is missing at ${OFX_ROOT}.\n"
    "Run: git submodule update --init --recursive")
endif()

# expat parses the plugin XML cache. The host does not read or write a cache, but
# HostSupport links against it and references its symbols from PluginCache, so the
# library has to be present.
find_package(EXPAT REQUIRED)

add_library(ofxhost_host STATIC
  ../host.cpp
  "${OFX_HOST_SRC}/ofxhBinary.cpp"
  "${OFX_HOST_SRC}/ofxhClip.cpp"
  "${OFX_HOST_SRC}/ofxhHost.cpp"
  "${OFX_HOST_SRC}/ofxhImageEffect.cpp"
  "${OFX_HOST_SRC}/ofxhImageEffectAPI.cpp"
  "${OFX_HOST_SRC}/ofxhInteract.cpp"
  "${OFX_HOST_SRC}/ofxhMemory.cpp"
  "${OFX_HOST_SRC}/ofxhParam.cpp"
  "${OFX_HOST_SRC}/ofxhPluginAPICache.cpp"
  "${OFX_HOST_SRC}/ofxhPluginCache.cpp"
  "${OFX_HOST_SRC}/ofxhPropertySuite.cpp"
  "${OFX_HOST_SRC}/ofxhUtilities.cpp"
)

target_include_directories(ofxhost_host PUBLIC
  "${OFX_INCLUDE}"
  "${OFX_HOST_INCLUDE}"
  "${CMAKE_CURRENT_SOURCE_DIR}/.."
  ${EXPAT_INCLUDE_DIRS}
)

target_link_libraries(ofxhost_host PUBLIC EXPAT::EXPAT)

# The host library is upstream code; its warnings are not ours to fix, and turning
# them into errors here would make an upstream change break this build.
if(MSVC)
  target_compile_options(ofxhost_host PRIVATE /W1)
else()
  target_compile_options(ofxhost_host PRIVATE -Wall -Wno-unused-parameter)
endif()

set_target_properties(ofxhost_host PROPERTIES OUTPUT_NAME "ofxhost")

# A stable location the Go build can name without knowing the generator.
set_target_properties(ofxhost_host PROPERTIES
  ARCHIVE_OUTPUT_DIRECTORY "${CMAKE_BINARY_DIR}/lib")

# --- The test fixture plugin -------------------------------------------------
#
# The host's tests used to depend on whichever plugins happened to be installed. That made
# them pass on one machine and fail on another, which is exactly what happened when the
# installed set changed from seventy plugins to six.
#
# This target builds a plugin this repository owns, so the tests describe behaviour rather
# than describe a vendor. It is built here rather than in a separate project so the one
# documented CMake step produces everything the Go build and its tests need.
#
# It is a MODULE library because a plugin is loaded, not linked. The .ofx bundle layout is
# what OFX discovery expects: <Name>.ofx.bundle/Contents/MacOS/<Name>.ofx on macOS and
# Contents/<arch>-<os>/ on Linux.

set(OFX_SUPPORT_LIB "${OFX_ROOT}/Support/Library")
set(OFX_SUPPORT_INCLUDE "${OFX_ROOT}/Support/include")

if(EXISTS "${OFX_SUPPORT_LIB}/ofxsImageEffect.cpp")
  # The fixture carries a Metal branch so the device path can be compared against the CPU
  # path. It is Objective-C for that reason: reading through an id<MTLBuffer> needs the
  # language, and the alternative is a fixture that cannot exercise the code it is there to
  # test. Apple only; elsewhere the file compiles its CPU branch.
  if(APPLE)
    enable_language(OBJCXX)
    set_source_files_properties(../testdata/fixture/plugin.mm PROPERTIES LANGUAGE OBJCXX)
  endif()
  add_library(ofxhost_fixture MODULE
    ../testdata/fixture/plugin.mm
    "${OFX_SUPPORT_LIB}/ofxsCore.cpp"
    "${OFX_SUPPORT_LIB}/ofxsImageEffect.cpp"
    "${OFX_SUPPORT_LIB}/ofxsInteract.cpp"
    "${OFX_SUPPORT_LIB}/ofxsLog.cpp"
    "${OFX_SUPPORT_LIB}/ofxsMultiThread.cpp"
    "${OFX_SUPPORT_LIB}/ofxsParams.cpp"
    "${OFX_SUPPORT_LIB}/ofxsProperty.cpp"
    "${OFX_SUPPORT_LIB}/ofxsPropertyValidation.cpp"
  )

  if(APPLE)
    target_link_libraries(ofxhost_fixture PRIVATE "-framework Metal" "-framework Foundation")
  endif()

  target_include_directories(ofxhost_fixture PRIVATE
    "${OFX_INCLUDE}"
    "${OFX_SUPPORT_INCLUDE}"
  )
  target_compile_options(ofxhost_fixture PRIVATE -Wall -Wno-unused-parameter)

  # The directory inside Contents/ that OFX discovery reads on each platform. Windows uses
  # Win64, which is OFX's name for it; a plugin placed under MacOS there is found by nothing.
  if(APPLE)
    set(OFX_FIXTURE_ARCH "MacOS")
  elseif(WIN32)
    set(OFX_FIXTURE_ARCH "Win64")
  elseif(CMAKE_SYSTEM_PROCESSOR MATCHES "^(aarch64|arm64)$")
    set(OFX_FIXTURE_ARCH "Linux-arm-64")
  else()
    set(OFX_FIXTURE_ARCH "Linux-x86-64")
  endif()

  # A plugin entry point is resolved by name at load time, so the symbol has to stay.
  set_target_properties(ofxhost_fixture PROPERTIES
    PREFIX ""
    SUFFIX ".ofx"
    OUTPUT_NAME "HostFixture"
    LIBRARY_OUTPUT_DIRECTORY
      "${CMAKE_BINARY_DIR}/fixture/HostFixture.ofx.bundle/Contents/${OFX_FIXTURE_ARCH}")

  add_custom_command(TARGET ofxhost_fixture POST_BUILD
    COMMAND ${CMAKE_COMMAND} -E make_directory
            "${CMAKE_BINARY_DIR}/fixture/HostFixture.ofx.bundle/Contents/Resources"
    COMMENT "Placing the fixture plugin in its OFX bundle")
else()
  message(WARNING "OpenFX Support sources not found; the fixture plugin will not be built")
endif()

# The bundled plugins live in their own bundle, built by the same configure. A plugin is
# loaded at runtime, not linked, so this is a separate target with its own dependencies -
# the Support library rather than HostSupport.
if(EXISTS "${CMAKE_CURRENT_SOURCE_DIR}/../../../../plugins/CMakeLists.txt")
  set(OFX_ROOT "${OFX_ROOT}" CACHE INTERNAL "OpenFX submodule root, shared with the plugins")
  # Not EXCLUDE_FROM_ALL: a build that skips it produces a host with no bundled plugins,
  # and the failure then looks like the plugins are broken rather than not built.
  add_subdirectory(
    "${CMAKE_CURRENT_SOURCE_DIR}/../../../../plugins"
    "${CMAKE_BINARY_DIR}/bundled"
  )
endif()
