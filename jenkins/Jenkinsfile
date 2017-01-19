#!/usr/bin/env groovy

class Build {
    static String AWS_SDK_GO_REPO_URL = "https://github.com/aws/aws-sdk-go.git"
    static String AWS_SDK_GO_SOURCE_DIRECTORY = "go/src/github.com/aws/aws-sdk-go"
    static String SOURCE_DIRECTORY = "go/src/tmobile/collectd_exporter"
}

node {
    echoParameters()

    docker.image("$GoLangVersion").inside {
        stage("Prepare Env") {
            env.GOARCH="$GOARCH"
            env.GOOS="$GOOS"
        }

        stage("Checkout Source") {
            dir("${Build.SOURCE_DIRECTORY}") {
                git branch: "$Branch", credentialsId: "$GitRepoCredentialId", url: "$GitRepoUrl"
            }
        }

        stage("Checkout AWS-SDK-GO") {
            dir("${Build.AWS_SDK_GO_SOURCE_DIRECTORY}") {
                git credentialsId: "$GitRepoCredentialId", url: "${Build.AWS_SDK_GO_REPO_URL}"
            }
        }

        stage("Build") {
            // construct the path to the go directory
            env.GOPATH= sh(script: 'pwd', returnStdout: true).trim() + "/go"

            // echo go environment variables
            sh "go env"

            dir("${Build.SOURCE_DIRECTORY}") {
                // build
                // output binary is at the $source directory
                sh "go build -o collectd-exporter-$GOOS-$GOARCH ${Build.SOURCE_DIRECTORY}/main.go"
            }
        }

        stage("Publish") {
            // TODO publish binary to artifactory
        }

         // Clean up workspace
         // TODO re-enable this after artifact is pushed to artifactory
         //step([$class: 'WsCleanup'])
    }
}

def echoParameters() {
  echo "GitRepoUrl: $GitRepoUrl"
  echo "GoLangVersion: $GoLangVersion"
  echo "GitRepoCredentialId: $GitRepoCredentialId"
  echo "GOOS: $GOOS"
  echo "GOARCH: $GOARCH"
  echo "Branch: $Branch"
}